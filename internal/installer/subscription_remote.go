package installer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

//go:embed subscription_remote.sh
var subscriptionRemoteScript []byte

func ValidateSubscriptionRemote(remote services.SubscriptionRemote) error {
	if err := ValidateNodeSSH(remote.SSH); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(remote.User) {
		return errors.New("remote SSH user is invalid")
	}
	if remote.Proxy != "nginx" && remote.Proxy != "caddy" {
		return errors.New("remote proxy must be nginx or caddy")
	}
	return nil
}

func (r *Real) scanSubscriptionSSH(ctx context.Context, remote services.SubscriptionRemote) (services.PanelResult, error) {
	if err := ValidateSubscriptionRemote(remote); err != nil {
		return services.PanelResult{}, err
	}
	key, fp, err := r.scanNodeSSH(ctx, remote.SSH)
	remote.SSH.HostKey = key
	return services.PanelResult{Remote: remote, Fingerprint: fp, Text: fp, TextRU: fp}, err
}

func (r *Real) remoteSubscriptionArgs(remote services.SubscriptionRemote) ([]string, error) {
	if err := ValidateSubscriptionRemote(remote); err != nil {
		return nil, err
	}
	// Reuse the established strict known_hosts handling, with no SSH multiplexing
	// or automatic retry of a mutating command after a connection failure.
	job := NodeJob{ID: strings.Repeat("0", 32)}
	job.Params.SSH = remote.SSH
	args, err := r.provisionSSHArgs(job, false)
	if err != nil {
		return nil, err
	}
	args = append(args, "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", remote.User+"@"+remote.SSH.Host)
	command := "bash -s"
	if remote.User != "root" {
		command = "sudo -n bash -s"
	}
	return append(args, command), nil
}

func renderRemoteSubscription(s State) ([]byte, []byte, string, error) {
	remote := s.Params.RemoteSubscription
	if remote == nil {
		return nil, nil, "", errors.New("remote subscription is not configured")
	}
	if s.LayoutVersion >= officialLayout {
		text := strings.Replace(templateFile("subscription.compose.yml"), "external: true", "external: false", 1)
		image := s.SubImage
		if image == "" {
			image = "remnawave/subscription-page:latest"
		}
		compose, err := patchComposeImage([]byte(text), "remnawave-subscription-page", image)
		if err != nil {
			return nil, nil, "", err
		}
		env := []byte(fmt.Sprintf("APP_PORT=3010\nREMNAWAVE_PANEL_URL=https://%s\nREMNAWAVE_API_TOKEN=%s\nTRUST_PROXY=1\n", s.Params.PanelDomain, s.SubToken))
		config := strings.NewReplacer("SUBSCRIPTION_PAGE_DOMAIN", s.Params.SubscriptionDomain, "ACME_EMAIL", s.Params.Email).Replace(templateFile(remote.Proxy + "-subscription.conf"))
		return compose, env, config, nil
	}
	image := s.SubImage
	if image == "" {
		image = "remnawave/subscription-page:latest"
	}
	sub := map[string]any{"image": image, "restart": "unless-stopped", "env_file": []string{".env"}, "ports": []string{"127.0.0.1:3010:3010"}, "networks": []string{"panel"}}
	doc := map[string]any{"name": "remnamanager-subscription", "services": map[string]any{"subscription": sub}, "networks": map[string]any{"panel": map[string]any{"name": "remnamanager-subscription_panel", "driver": "bridge"}}}
	compose, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, "", err
	}
	env := []byte(fmt.Sprintf("APP_PORT=3010\nREMNAWAVE_PANEL_URL=https://%s\nREMNAWAVE_API_TOKEN=%s\nTRUST_PROXY=1\n", s.Params.PanelDomain, s.SubToken))
	domain := s.Params.SubscriptionDomain
	config := fmt.Sprintf("{\n email %s\n admin 127.0.0.1:2019\n}\n%s {\n encode zstd gzip\n reverse_proxy subscription:3010 {\n header_up X-Forwarded-For {remote_host}\n header_up -X-Forwarded-Prefix\n }\n}\n", s.Params.Email, domain)
	if remote.Proxy == "nginx" {
		config = fmt.Sprintf("map $http_upgrade $connection_upgrade { default upgrade; '' close; }\nserver { listen 80; server_name %s; location ^~ /.well-known/acme-challenge/ { root /var/www/remnamanager-acme; } location / { return 301 https://%s$request_uri; } }\nserver { listen 443 ssl; http2 on; server_name %s; ssl_protocols TLSv1.2 TLSv1.3; ssl_certificate /etc/nginx/certs/%s/fullchain.pem; ssl_certificate_key /etc/nginx/certs/%s/privkey.pem; location / { proxy_pass http://subscription:3010; proxy_http_version 1.1; proxy_set_header Host $host; proxy_set_header X-Forwarded-For $remote_addr; proxy_set_header X-Real-IP $remote_addr; proxy_set_header X-Forwarded-Proto https; proxy_set_header X-Forwarded-Host $host; proxy_set_header X-Forwarded-Port 443; proxy_read_timeout 60s; proxy_send_timeout 60s; } }\nserver { listen 443 ssl default_server; ssl_reject_handshake on; }\n", domain, domain, domain, domain, domain)
	}
	return compose, env, config, nil
}

func (r *Real) remoteSubscription(ctx context.Context, action, value string) (string, error) {
	remote := r.state.Params.RemoteSubscription
	if remote == nil {
		return "", errors.New("remote subscription is not configured")
	}
	args, err := r.remoteSubscriptionArgs(*remote)
	if err != nil {
		return "", err
	}
	compose, env, config, err := renderRemoteSubscription(r.state)
	if err != nil {
		return "", err
	}
	if (action == "update" || action == "image") && value != "" {
		if err = ValidateSubscriptionImage(value); err != nil {
			return "", err
		}
	}
	values := map[string]string{"LAYOUT": strconv.Itoa(r.state.LayoutVersion), "PROXY_COMPOSE": templateFile(remote.Proxy + ".compose.yml"), "CA": panelCA(r.state.Params), "ACTION": action, "VALUE": value, "COMPOSE": string(compose), "ENV": string(env), "CONFIG": config, "DOMAIN": r.state.Params.SubscriptionDomain, "PANEL": r.state.Params.PanelDomain, "TOKEN": r.state.SubToken, "EMAIL": r.state.Params.Email, "PROXY": remote.Proxy, "SETUP": string(setupScript), "ACME": string(r.acmeScript())}
	var input strings.Builder
	input.WriteString("set +x\n")
	for name, value := range values {
		fmt.Fprintf(&input, "RM_%s_B64='%s'\n", name, base64.StdEncoding.EncodeToString([]byte(value)))
	}
	input.Write(subscriptionRemoteScript)
	data, err := r.Runner.Run(ctx, []byte(input.String()), "ssh", args...)
	if r.state.Removed {
		return r.safeOutput(data), err
	}
	if action == "status" {
		return r.safeOutput(data), err
	}
	if action == "preflight" {
		return r.safeOutput(data), err
	}
	if err != nil {
		r.state.RemoteLastError = r.safeOutput(data)
		_ = saveState(statePath(r.Root), r.state)
		return "", fmt.Errorf("remote subscription SSH/command failure; check host key, authentication, sudo and remote Docker; no automatic retry: %w; %s", err, r.state.RemoteLastError)
	}
	r.state.RemoteLastError = ""
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return "", err
	}
	return r.safeOutput(data), nil
}

func (r *Real) remoteSSHDetails() string {
	remote := r.state.Params.RemoteSubscription
	if remote == nil {
		return "Subscription is local / Подписка на этом сервере"
	}
	return fmt.Sprintf("SSH: %s@%s:%s\nKey: %s\nProxy: %s\nLast error: %s", remote.User, remote.SSH.Host, strconv.Itoa(remote.SSH.Port), filepath.Clean(remote.SSH.IdentityFile), remote.Proxy, r.state.RemoteLastError)
}
