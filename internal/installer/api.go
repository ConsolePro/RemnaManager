package installer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func apiCall(ctx context.Context, client *http.Client, base, host, method, path, token string, body any) (map[string]json.RawMessage, error) {
	raw, err := apiResponse(ctx, client, base, host, method, path, token, body)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, errors.New("expected a panel API object response")
	}
	return data, nil
}

// apiResponse supports object, array and empty (202/204) endpoint responses.
func apiResponse(ctx context.Context, client *http.Client, base, host, method, path, token string, body any) (json.RawMessage, error) {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return nil, errors.New("panel API response is unreadable or too large")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
			return nil, nil
		}
		return nil, errors.New("empty panel API response")
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("invalid panel API response")
	}
	if envelope.Response == nil {
		return nil, errors.New("missing panel API response envelope")
	}
	return envelope.Response, nil
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
	for {
		err := r.checkPanelProtection(wait)
		if err == nil {
			break
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("public HTTPS/protection check failed: %w", err)
		case <-time.After(3 * time.Second):
		}
	}
	// A bare subscription URL intentionally closes its socket. Check its public
	// certificate without a real user's shortUuid or relaxed TLS verification.
	for {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(wait, "tcp", net.JoinHostPort(r.state.Params.SubscriptionDomain, "443"))
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("subscription public TLS verification timed out")
		case <-time.After(2 * time.Second):
		}
	}
}
