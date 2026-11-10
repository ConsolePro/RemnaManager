package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func apiCall(ctx context.Context, client *http.Client, base, host, method, path, token string, body any) (map[string]json.RawMessage, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytesReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Forwarded-Host", host)
	if strings.HasPrefix(path, "/tokens") {
		req.Header.Set("X-Remnawave-Client-Type", "browser")
	}
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("panel API connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("panel API %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	var data map[string]json.RawMessage
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, errors.New("invalid panel API response")
	}
	if wrapped, ok := data["response"]; ok {
		if err = json.Unmarshal(wrapped, &data); err != nil {
			return nil, errors.New("invalid panel response envelope")
		}
	}
	return data, nil
}
func responseString(data map[string]json.RawMessage, key string) (string, error) {
	var s string
	err := json.Unmarshal(data[key], &s)
	if err != nil || s == "" {
		return "", fmt.Errorf("panel response is missing %s", key)
	}
	return s, nil
}

func (r *Real) bootstrap(ctx context.Context) error {
	if err := r.waitAPI(ctx); err != nil {
		return err
	}
	s := &r.state
	if s.ManagerToken != "" && s.SubToken != "" {
		for _, probe := range []struct{ path, token string }{{"/nodes", s.ManagerToken}, {"/system/stats", s.SubToken}} {
			req, e := http.NewRequestWithContext(ctx, "GET", r.APIBase+probe.path, nil)
			if e != nil {
				return e
			}
			req.Header.Set("Authorization", "Bearer "+probe.token)
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			resp, e := r.Client.Do(req)
			if e != nil {
				return errors.New("cannot validate saved API tokens")
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				return errors.New("saved API token is no longer valid; restore state/database or perform explicit credential recovery")
			}
		}
		return nil
	}
	status, err := apiCall(ctx, r.Client, r.APIBase, s.Params.PanelDomain, "GET", "/auth/status", "", nil)
	if err != nil {
		return err
	}
	var allowed bool
	_ = json.Unmarshal(status["isRegisterAllowed"], &allowed)
	action := "/auth/login"
	if allowed {
		action = "/auth/register"
	}
	auth, err := apiCall(ctx, r.Client, r.APIBase, s.Params.PanelDomain, "POST", action, "", map[string]string{"username": s.Params.Admin, "password": s.AdminPassword})
	if err != nil {
		return err
	}
	token, err := responseString(auth, "accessToken")
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name   string
		scopes []string
		target *string
	}{
		{"RemnaManager", []string{"*"}, &s.ManagerToken},
		{"RemnaManager Subpage", []string{"subscriptions:read", "subscription-page-configs:read", "system:read"}, &s.SubToken},
	} {
		if *entry.target != "" {
			continue
		}
		data, err := apiCall(ctx, r.Client, r.APIBase, s.Params.PanelDomain, "POST", "/tokens", token, map[string]any{"name": entry.name, "expiresInDays": 3650, "scopes": entry.scopes})
		if err != nil {
			return err
		}
		*entry.target, err = responseString(data, "token")
		if err != nil {
			return err
		}
		if err = saveState(statePath(r.Root), *s); err != nil {
			return err
		}
	}
	return writeFile(r.Stack+"/subscription.env", renderSubEnv(*s), 0600)
}

func (r *Real) waitAPI(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		if _, err := apiCall(wait, r.Client, r.APIBase, r.state.Params.PanelDomain, "GET", "/auth/status", "", nil); err == nil {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("panel API did not become ready; see private install log")
		case <-time.After(2 * time.Second):
		}
	}
}
func (r *Real) waitHTTPS(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	targets := []string{"https://" + r.state.Params.PanelDomain + "/api/auth/status", "https://" + r.state.Params.SubscriptionDomain + "/"}
	for i, target := range targets {
		for {
			req, e := http.NewRequestWithContext(wait, "GET", target, nil)
			if e != nil {
				return e
			}
			resp, e := r.Client.Do(req)
			if e == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && (resp.StatusCode < 400 || i == 1 && resp.StatusCode == 404) {
					break
				}
			}
			select {
			case <-wait.Done():
				return fmt.Errorf("HTTPS check failed for %s; verify DNS, 80/443 and ACME", target)
			case <-time.After(3 * time.Second):
			}
		}
	}
	return nil
}
