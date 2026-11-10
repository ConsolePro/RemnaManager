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

func RenderCaddy(p services.PanelParameters) string {
	return fmt.Sprintf(`# Managed by RemnaManager Go
{
 email %q
 admin 127.0.0.1:2019
}
%s {
 reverse_proxy 127.0.0.1:3000 {
  header_up X-Forwarded-Proto https
  header_up X-Forwarded-For {remote_host}
 }
}
%s {
 reverse_proxy 127.0.0.1:3010 {
  header_up X-Forwarded-Proto https
  header_up X-Forwarded-For {remote_host}
 }
}
`, p.Email, p.PanelDomain, p.SubscriptionDomain)
}
func RenderNginx(p services.PanelParameters, tls bool) string {
	var b strings.Builder
	b.WriteString("# Managed by RemnaManager Go\nmap $http_upgrade $rm_go_connection { default upgrade; '' close; }\n")
	for _, site := range []struct {
		domain string
		port   int
	}{{p.PanelDomain, 3000}, {p.SubscriptionDomain, 3010}} {
		fmt.Fprintf(&b, "server {\n listen 80; listen [::]:80;\n server_name %s;\n location ^~ /.well-known/acme-challenge/ { root /var/www/remnamanager-acme; }\n", site.domain)
		if tls {
			b.WriteString(" location / { return 301 https://$host$request_uri; }\n}\n")
			fmt.Fprintf(&b, "server {\n listen 443 ssl http2; listen [::]:443 ssl http2;\n server_name %s;\n ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;\n ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;\n ssl_protocols TLSv1.2 TLSv1.3;\n", site.domain, site.domain, site.domain)
		} else {
			b.WriteString(" location / { return 503; }\n}\n")
			continue
		}
		fmt.Fprintf(&b, ` server_tokens off;
 location / {
  proxy_pass http://127.0.0.1:%d;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Real-IP $remote_addr;
  proxy_set_header X-Forwarded-For $remote_addr;
  proxy_set_header X-Forwarded-Proto https;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $rm_go_connection;
  proxy_read_timeout 300s;
 }
}
`, site.port)
	}
	return b.String()
}

func (r *Real) configureProxy(ctx context.Context) error {
	p := r.state.Params
	if p.Proxy == "caddy" {
		return r.proxyFile(ctx, "/etc/caddy/Caddyfile", []byte(RenderCaddy(p)), "caddy", []string{"caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"})
	}
	if e := os.MkdirAll("/var/www/remnamanager-acme/.well-known/acme-challenge", 0755); e != nil {
		return e
	}
	if e := os.Chmod("/var/www/remnamanager-acme", 0755); e != nil {
		return e
	}
	// Remove only the distribution default installed by this fresh-host workflow.
	if target, e := os.Readlink("/etc/nginx/sites-enabled/default"); e == nil && filepath.Clean(target) == "/etc/nginx/sites-available/default" {
		if e = os.Remove("/etc/nginx/sites-enabled/default"); e != nil {
			return e
		}
	}
	path := "/etc/nginx/conf.d/remnamanager-go.conf"
	// HTTP bootstrap has no proxy forwarding, only ACME; panel credentials never
	// travel in cleartext before the first certificate has been issued.
	bootstrap := RenderNginx(p, false)
	if e := r.proxyFile(ctx, path, []byte(bootstrap), "nginx", []string{"nginx", "-t"}); e != nil {
		return e
	}
	for _, domain := range []string{p.PanelDomain, p.SubscriptionDomain} {
		if _, e := r.Runner.Run(ctx, nil, "certbot", "certonly", "--webroot", "-w", "/var/www/remnamanager-acme", "-d", domain, "--email", p.Email, "--agree-tos", "--non-interactive", "--keep-until-expiring"); e != nil {
			return e
		}
	}
	hook := "#!/bin/sh\nset -eu\n/usr/sbin/nginx -t\n/bin/systemctl reload nginx\n"
	if e := writeFile("/etc/letsencrypt/renewal-hooks/deploy/remnamanager-go-nginx", []byte(hook), 0700); e != nil {
		return e
	}
	if _, e := r.Runner.Run(ctx, nil, "systemctl", "enable", "--now", "certbot.timer"); e != nil {
		return e
	}
	return r.proxyFile(ctx, path, []byte(RenderNginx(p, true)), "nginx", []string{"nginx", "-t"})
}

func (r *Real) proxyFile(ctx context.Context, path string, data []byte, service string, check []string) error {
	old, readErr := os.ReadFile(path)
	// Persist a private backup as well as restoring on validation/reload failure.
	if readErr == nil {
		if e := writeFile(filepath.Join(r.Root, service+".previous"), old, 0600); e != nil {
			return e
		}
	}
	if e := writeFile(path, data, 0644); e != nil {
		return e
	}
	restore := func() {
		if readErr == nil {
			_ = writeFile(path, old, 0644)
		} else {
			_ = os.Remove(path)
		}
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = r.Runner.Run(restoreCtx, nil, "systemctl", "reload", service)
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
