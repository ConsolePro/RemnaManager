package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func proxyDomains(p services.PanelParameters) []string {
	domains := []string{p.PanelDomain}
	if p.RemoteSubscription == nil {
		domains = append(domains, p.SubscriptionDomain)
	}
	if protectionMode(p) == "tinyauth" {
		domains = append(domains, p.AuthDomain)
	}
	return domains
}

func caddyUpstream(port int) string {
	apiHeader := " header_up -X-Api-Key\n"
	if port == 3030 {
		apiHeader = ""
	} // MFA gateway consumes its own API key.
	return fmt.Sprintf(`reverse_proxy 127.0.0.1:%d {
 header_up X-Forwarded-Proto https
 header_up X-Forwarded-For {remote_host}
 header_up X-Forwarded-Port 443
 header_up -X-Forwarded-Prefix
 header_up -Remote-User
 header_up -Remote-Email
 header_up -Remote-Groups
%s
}
`, port, apiHeader)
}

func RenderCaddy(s State) string {
	p := s.Params
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by RemnaManager Go\n{\n email %q\n admin 127.0.0.1:2019\n}\n%s {\n encode zstd gzip\n header Referrer-Policy no-referrer\n", p.Email, p.PanelDomain)
	if validateProtectionState(s) != nil {
		b.WriteString(" respond Forbidden 403\n")
	} else if protectionMode(p) == "cookie" {
		fmt.Fprintf(&b, ` route {
  @entry {
   path /auth/login
   query %s=%s
  }
  handle @entry {
   header Set-Cookie "%s=%s; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=604800"
   header Cache-Control no-store
   redir * /auth/login 303
  }
  @allowed header_regexp rm_gate Cookie "(^|;[ \\t]*)%s=%s(;|$)"
  handle @allowed {
   %s
  }
  respond Forbidden 403
 }
`, s.CookieKey, s.CookieValue, s.CookieKey, s.CookieValue, s.CookieKey, s.CookieValue, caddyUpstream(3000))
	} else if protectionMode(p) == "mfa" {
		b.WriteString(caddyUpstream(3030))
	} else {
		b.WriteString(" route {\n forward_auth 127.0.0.1:3020 {\n uri /api/auth/caddy\n header_up -X-Original-URL\n header_up -X-Original-Method\n header_up Authorization {http.request.header.X-Api-Key}\n }\n" + caddyUpstream(3000) + " }\n")
	}
	b.WriteString("}\n")
	// The remote subpage uses its scoped Bearer token on read-only API routes.
	// It never receives the panel's gate cookie, TinyAuth or MFA credentials.
	if p.RemoteSubscription == nil {
		fmt.Fprintf(&b, "%s {\n encode zstd gzip\n %s}\n", p.SubscriptionDomain, caddyUpstream(3010))
	}
	if protectionMode(p) == "tinyauth" {
		fmt.Fprintf(&b, "%s {\n header Referrer-Policy no-referrer\n %s}\n", p.AuthDomain, caddyUpstream(3020))
	}
	text := b.String()
	if p.RemoteSubscription != nil && s.SubToken != "" && validateProtectionState(s) == nil {
		gateway := fmt.Sprintf(" route {\n @subscription_api {\n method GET\n path /api/subscriptions/* /api/subscription-page-configs /api/subscription-page-configs/* /api/system/stats\n header Authorization \"Bearer %s\"\n }\n handle @subscription_api {\n %s\n }\n handle {\n", s.SubToken, caddyUpstream(3000))
		start := strings.Index(text, " header Referrer-Policy no-referrer\n")
		if start >= 0 {
			start += len(" header Referrer-Policy no-referrer\n")
			end := strings.Index(text[start:], "\n}\n")
			if end >= 0 {
				text = text[:start] + gateway + text[start:start+end] + "\n }\n }" + text[start+end:]
			}
		}
	}
	return text
}

func nginxUpstream(port int) string {
	apiHeader := "  proxy_set_header X-Api-Key \"\";\n"
	if port == 3030 {
		apiHeader = ""
	}
	return fmt.Sprintf(`  proxy_pass http://127.0.0.1:%d;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Real-IP $remote_addr;
  proxy_set_header X-Forwarded-For $remote_addr;
  proxy_set_header X-Forwarded-Proto https;
  proxy_set_header X-Forwarded-Host $host;
  proxy_set_header X-Forwarded-Port 443;
  proxy_set_header X-Forwarded-Prefix "";
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $rm_go_connection;
  proxy_set_header Remote-User "";
  proxy_set_header Remote-Email "";
  proxy_set_header Remote-Groups "";
  proxy_read_timeout 300s;
%s`, port, apiHeader)
}

func RenderNginx(s State, tls bool) string {
	p := s.Params
	var b strings.Builder
	b.WriteString("# Managed by RemnaManager Go\nmap $http_upgrade $rm_go_connection { default upgrade; '' close; }\n")
	b.WriteString("gzip on;\ngzip_vary on;\ngzip_min_length 256;\ngzip_proxied any;\ngzip_types application/javascript application/json application/wasm application/xml image/svg+xml text/css text/plain;\n")
	protected := validateProtectionState(s) == nil
	if tls && protected && protectionMode(p) == "cookie" {
		// Regex entries avoid Nginx's default 64-byte map hash bucket limit.
		fmt.Fprintf(&b, "map $cookie_%s $rm_go_gate_cookie { default 0; ~^%s$ 1; }\nmap $arg_%s $rm_go_gate_query { default 0; ~^%s$ 1; }\n", s.CookieKey, s.CookieValue, s.CookieKey, s.CookieValue)
	}
	for i, domain := range proxyDomains(p) {
		fmt.Fprintf(&b, "server {\n listen 80; listen [::]:80;\n server_name %s;\n access_log off;\n location ^~ /.well-known/acme-challenge/ { root /var/www/remnamanager-acme; }\n", domain)
		if tls {
			// Redirect to the configured domain, never an untrusted Host header.
			fmt.Fprintf(&b, " location / { return 301 https://%s$request_uri; }\n}\n", domain)
			fmt.Fprintf(&b, "server {\n listen 443 ssl http2; listen [::]:443 ssl http2;\n server_name %s;\n ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;\n ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;\n ssl_protocols TLSv1.2 TLSv1.3;\n", domain, domain, domain)
		} else {
			b.WriteString(" location / { return 503; }\n}\n")
			continue
		}
		b.WriteString(" server_tokens off;\n")
		if i != 0 {
			port := 3010
			if domain == p.AuthDomain {
				port = 3020
			}
			b.WriteString(" location / {\n" + nginxUpstream(port) + " }\n}\n")
			continue
		}
		b.WriteString(" access_log off;\n add_header Referrer-Policy no-referrer always;\n")
		if protected && p.RemoteSubscription != nil && s.SubToken != "" {
			fmt.Fprintf(&b, " location ~ ^/api/(subscriptions/|subscription-page-configs(/|$)|system/stats$) {\n if ($request_method != GET) { return 403; }\n if ($http_authorization != \"Bearer %s\") { return 403; }\n%s }\n", s.SubToken, nginxUpstream(3000))
		}
		if !protected {
			b.WriteString(" location / { return 403; }\n}\n")
			continue
		}
		if protectionMode(p) == "cookie" {
			fmt.Fprintf(&b, ` location = /auth/login {
  if ($rm_go_gate_query = 1) {
   add_header Set-Cookie "%s=%s; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=604800" always;
   add_header Referrer-Policy no-referrer always;
   add_header Cache-Control no-store always;
   return 303 /auth/login;
  }
  if ($rm_go_gate_cookie = 0) { return 403; }
%s }
 location / {
  if ($rm_go_gate_cookie = 0) { return 403; }
%s }
}
`, s.CookieKey, s.CookieValue, nginxUpstream(3000), nginxUpstream(3000))
		} else if protectionMode(p) == "mfa" {
			b.WriteString(" location / {\n" + nginxUpstream(3030) + " }\n}\n")
		} else {
			b.WriteString(` location = /_rm_tinyauth {
  internal;
  proxy_pass http://127.0.0.1:3020/api/auth/nginx;
  proxy_pass_request_body off;
  proxy_set_header Content-Length "";
  proxy_set_header Authorization $http_x_api_key;
  proxy_set_header Host $host;
  proxy_set_header X-Original-URL https://$host$request_uri;
  proxy_set_header X-Original-Method $request_method;
  proxy_set_header X-Forwarded-Proto https;
  proxy_set_header X-Forwarded-Host "";
  proxy_set_header X-Forwarded-Uri "";
  proxy_set_header X-Forwarded-For $remote_addr;
  proxy_set_header X-Real-IP $remote_addr;
 }
 location / {
  auth_request /_rm_tinyauth;
  auth_request_set $rm_go_auth_location $upstream_http_x_tinyauth_location;
  error_page 401 403 =302 $rm_go_auth_location;
` + nginxUpstream(3000) + " }\n}\n")
		}
	}
	return b.String()
}

func (r *Real) configureProxy(ctx context.Context) error {
	if r.state.ProxyRuntime == "docker" {
		return r.configureDockerProxy(ctx)
	}
	p := r.state.Params
	if err := validateProtectionState(r.state); err != nil {
		return err
	}
	if p.Proxy == "caddy" {
		services.ReportProgress(ctx, "Настраиваю Caddy и автоматический HTTPS", "Configuring Caddy and automatic HTTPS")
		path := r.systemPath("/etc/caddy/Caddyfile")
		return r.proxyFile(ctx, path, []byte(RenderCaddy(r.state)), "caddy", []string{"caddy", "validate", "--config", path, "--adapter", "caddyfile"})
	}
	if e := os.MkdirAll(r.systemPath("/var/www/remnamanager-acme/.well-known/acme-challenge"), 0755); e != nil {
		return e
	}
	if e := os.Chmod(r.systemPath("/var/www/remnamanager-acme"), 0755); e != nil {
		return e
	}
	// Remove only the distribution default installed by this fresh-host workflow.
	if target, e := os.Readlink(r.systemPath("/etc/nginx/sites-enabled/default")); e == nil && filepath.Clean(target) == "/etc/nginx/sites-available/default" {
		if e = os.Remove(r.systemPath("/etc/nginx/sites-enabled/default")); e != nil {
			return e
		}
	}
	path := r.systemPath("/etc/nginx/conf.d/remnamanager-go.conf")
	// HTTP bootstrap has no proxy forwarding, only ACME; panel credentials never
	// travel in cleartext before the first certificate has been issued.
	bootstrap := RenderNginx(r.state, false)
	services.ReportProgress(ctx, "Настраиваю Nginx для проверки доменов ACME", "Configuring Nginx for ACME domain validation")
	if e := r.proxyFile(ctx, path, []byte(bootstrap), "nginx", []string{"nginx", "-t"}); e != nil {
		return e
	}
	for _, domain := range proxyDomains(p) {
		services.ReportProgress(ctx, "Получаю HTTPS-сертификат для "+domain, "Obtaining an HTTPS certificate for "+domain)
		if _, e := r.Runner.Run(ctx, nil, "certbot", "certonly", "--webroot", "-w", "/var/www/remnamanager-acme", "-d", domain, "--email", p.Email, "--agree-tos", "--non-interactive", "--keep-until-expiring"); e != nil {
			return e
		}
	}
	hook := "#!/bin/sh\nset -eu\n/usr/sbin/nginx -t\n/bin/systemctl reload nginx\n"
	services.ReportProgress(ctx, "Включаю HTTPS и автоматическое обновление сертификатов", "Enabling HTTPS and automatic certificate renewal")
	if e := writeFile(r.systemPath("/etc/letsencrypt/renewal-hooks/deploy/remnamanager-go-nginx"), []byte(hook), 0700); e != nil {
		return e
	}
	if _, e := r.Runner.Run(ctx, nil, "systemctl", "enable", "--now", "certbot.timer"); e != nil {
		return e
	}
	return r.proxyFile(ctx, path, []byte(RenderNginx(r.state, true)), "nginx", []string{"nginx", "-t"})
}

func (r *Real) proxyFile(ctx context.Context, path string, data []byte, service string, check []string) error {
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	mode := os.FileMode(0600)
	if service == "caddy" {
		mode = 0640
	}
	writeConfig := func(writeCtx context.Context, content []byte) error {
		if err := writeFile(path, content, mode); err != nil {
			return err
		}
		if service == "caddy" {
			_, err := r.Runner.Run(writeCtx, nil, "chown", "root:caddy", path)
			return err
		}
		return nil
	}
	// Persist a private backup as well as restoring on validation/reload failure.
	if readErr == nil {
		if e := writeFile(filepath.Join(r.Root, service+".previous"), old, 0600); e != nil {
			return e
		}
	}
	restore := func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if readErr == nil {
			_ = writeConfig(restoreCtx, old)
		} else {
			_ = os.Remove(path)
		}
		_, _ = r.Runner.Run(restoreCtx, nil, "systemctl", "reload", service)
	}
	if e := writeConfig(ctx, data); e != nil {
		restore()
		return e
	}
	if _, e := r.Runner.Run(ctx, nil, check[0], check[1:]...); e != nil {
		restore()
		return e
	}
	if _, e := r.Runner.Run(ctx, nil, "systemctl", "enable", "--now", service); e != nil {
		restore()
		return e
	}
	if _, e := r.Runner.Run(ctx, nil, "systemctl", "reload", service); e != nil {
		restore()
		return e
	}
	return nil
}
