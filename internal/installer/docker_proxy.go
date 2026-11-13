package installer

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed official_acme.sh
var officialACMEIssueScript string

//go:embed panel_acme.sh
var legacyACMEIssueScript string

//go:embed panel_acme_cron.sh
var acmeRenewalScript string

var panelACMEScript = []byte(officialACMEIssueScript + "\n" + acmeRenewalScript)
var legacyPanelACMEScript = []byte(legacyACMEIssueScript + "\n" + acmeRenewalScript)

func (r *Real) ensureACMERenewal(ctx context.Context) error {
	// Imported certificates may have a separate renewal provider.
	if _, err := os.Stat(filepath.Join(r.Root, "acme", "acme.sh")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	_, err := r.Runner.Run(ctx, []byte(acmeRenewalScript), "bash", "-s", "--", r.Root)
	return err
}

func (r *Real) acmeScript() []byte {
	if r.state.LayoutVersion >= officialLayout {
		return panelACMEScript
	}
	return legacyPanelACMEScript
}

func dockerProxyConfig(s State, tls bool) string {
	if s.LayoutVersion >= officialLayout {
		return renderOfficialProxy(s)
	}
	text := RenderCaddy(s)
	if s.Params.Proxy == "nginx" {
		text = RenderNginx(s, tls)
		text = strings.ReplaceAll(text, "listen 443 ssl http2; listen [::]:443 ssl http2;", "listen 443 ssl;\n http2 on;")
		text = strings.ReplaceAll(text, "listen 80; listen [::]:80;", "listen 80;")
		text = strings.ReplaceAll(text, "/etc/letsencrypt/live/", "/etc/nginx/certs/")
		if tls {
			text += "server { listen 443 ssl default_server; server_name _; ssl_reject_handshake on; }\n"
		}
	}
	for from, to := range map[string]string{"127.0.0.1:3000": "remnawave:3000", "127.0.0.1:3010": "subscription:3010", "127.0.0.1:3020": "tinyauth:3000", "127.0.0.1:3030": "mfa-gateway:8080"} {
		text = strings.ReplaceAll(text, from, to)
	}
	if s.Params.Proxy == "nginx" {
		text = "resolver 127.0.0.11 valid=10s ipv6=off;\n" + text
		for _, address := range []string{"remnawave:3000", "subscription:3010", "tinyauth:3000", "mfa-gateway:8080"} {
			text = strings.ReplaceAll(text, "proxy_pass http://"+address+";", "set $rm_go_upstream http://"+address+";\n  proxy_pass $rm_go_upstream;")
		}
	}
	return text
}

func (r *Real) proxyCompose(ctx context.Context, proxy string, args ...string) ([]byte, error) {
	dir := filepath.Join(r.Stack, proxy)
	all := append([]string{"compose", "--project-directory", dir, "-f", filepath.Join(dir, "docker-compose.yml")}, officialArgs(r.state, args)...)
	return r.Runner.Run(ctx, nil, "docker", all...)
}

func (r *Real) writeDockerProxy(tls bool) error {
	if r.state.LayoutVersion >= officialLayout {
		return r.writeOfficialProxy()
	}
	proxy := r.state.Params.Proxy
	if proxy != "nginx" && proxy != "caddy" {
		return errors.New("invalid proxy")
	}
	dir := filepath.Join(r.Stack, proxy)
	name, image := "nginx.conf", "nginx:1.30-alpine"
	// Mount directories: atomic file replacement must be visible on reload.
	// Binding an individual file pins its old inode across writeFile/rename.
	mounts := []string{"./:/etc/nginx/conf.d:ro", "./certs:/etc/nginx/certs:ro", "./acme:/var/www/remnamanager-acme:ro"}
	ports := []string{"80:80", "443:443"}
	if proxy == "caddy" {
		name, image = "Caddyfile", "caddy:2-alpine"
		mounts = []string{"./:/etc/caddy:ro", "./data:/data", "./config:/config", "../nginx/certs:/etc/nginx/certs:ro", "../nginx/acme:/var/www/remnamanager-acme:ro"}
		ports = append(ports, "443:443/udp")
	}
	svc := map[string]any{"image": image, "restart": "unless-stopped", "ports": ports, "volumes": mounts, "networks": []string{"panel"}, "logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}}}
	doc := map[string]any{"name": "remnamanager-" + proxy, "services": map[string]any{proxy: svc}, "networks": map[string]any{"panel": map[string]any{"external": true, "name": "remnamanager-panel_panel"}}}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	for _, subdir := range []string{"nginx/certs", "nginx/acme/.well-known/acme-challenge", "caddy/data", "caddy/config"} {
		if err = os.MkdirAll(filepath.Join(r.Stack, subdir), 0700); err != nil {
			return err
		}
	}
	for _, subdir := range []string{"nginx/acme", "nginx/acme/.well-known", "nginx/acme/.well-known/acme-challenge"} {
		if err = os.Chmod(filepath.Join(r.Stack, subdir), 0755); err != nil {
			return err
		}
	}
	if err = writeFile(filepath.Join(dir, "docker-compose.yml"), data, 0600); err != nil {
		return err
	}
	text := dockerProxyConfig(r.state, tls)
	// Reuse existing Nginx certificates in Caddy without asking ACME to reissue.
	if proxy == "caddy" {
		for _, domain := range proxyDomains(r.state.Params) {
			if certificateUsable(filepath.Join(r.Stack, "nginx", "certs", domain), domain) == nil {
				text = strings.Replace(text, domain+" {\n", domain+" {\n tls /etc/nginx/certs/"+domain+"/fullchain.pem /etc/nginx/certs/"+domain+"/privkey.pem\n", 1)
				text += "\nhttp://" + domain + " {\n handle /.well-known/acme-challenge/* {\n root * /var/www/remnamanager-acme\n file_server\n }\n handle {\n redir https://" + domain + "{uri} permanent\n }\n}\n"
			}
		}
	}
	return writeFile(filepath.Join(dir, name), []byte(text), 0600)
}

func (r *Real) validateDockerProxy(ctx context.Context) error {
	proxy := r.state.Params.Proxy
	args := []string{"run", "--rm", "--no-deps", proxy, "nginx", "-t"}
	if proxy == "caddy" {
		args = []string{"run", "--rm", "--no-deps", proxy, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"}
	}
	_, err := r.proxyCompose(ctx, proxy, args...)
	return err
}

func (r *Real) configureDockerProxy(ctx context.Context) error {
	if r.state.LayoutVersion >= officialLayout {
		return r.configureOfficialProxy(ctx)
	}
	proxy := r.state.Params.Proxy
	if err := validateProtectionState(r.state); err != nil {
		return err
	}
	if proxy == "nginx" {
		ready := true
		for _, domain := range proxyDomains(r.state.Params) {
			if certificateUsable(filepath.Join(r.Stack, "nginx", "certs", domain), domain) != nil {
				ready = false
			}
		}
		if !ready {
			if err := r.writeDockerProxy(false); err != nil {
				return err
			}
			if _, err := r.proxyCompose(ctx, proxy, "up", "-d"); err != nil {
				return err
			}
			if _, err := r.Runner.Run(ctx, legacyPanelACMEScript, "bash", "-s", "--", r.Root, r.Stack, r.state.Params.Email, panelCA(r.state.Params), strings.Join(proxyDomains(r.state.Params), ",")); err != nil {
				return err
			}
		} else if err := r.ensureACMERenewal(ctx); err != nil {
			return err
		}
	}
	if err := r.writeDockerProxy(true); err != nil {
		return err
	}
	if err := r.validateDockerProxy(ctx); err != nil {
		return err
	}
	if _, err := r.proxyCompose(ctx, proxy, "up", "-d"); err != nil {
		return err
	}
	if proxy == "caddy" {
		_, err := r.proxyCompose(ctx, proxy, "exec", "-T", proxy, "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		return err
	}
	_, err := r.proxyCompose(ctx, proxy, "exec", "-T", proxy, "nginx", "-s", "reload")
	return err
}

func (r *Real) changeProxy(ctx context.Context, proxy string) error {
	if r.state.ProxyRecovery != "" {
		return r.recoverProxyMigration(ctx)
	}
	if r.state.LayoutVersion >= officialLayout {
		return r.changeOfficialProxy(ctx, proxy)
	}
	if proxy != "nginx" && proxy != "caddy" {
		return errors.New("proxy must be nginx or caddy")
	}
	if proxy == r.state.Params.Proxy && r.state.ProxyRuntime == "docker" {
		return nil
	}
	old := r.state
	backup, err := r.backup(ctx, "pre-proxy-migration")
	if err != nil {
		return err
	}
	if _, err = r.Runner.Run(ctx, setupScript, "bash", "-s", "--", "docker-proxy", runtime.GOARCH); err != nil {
		return err
	}
	if old.Params.Proxy == "caddy" && old.ProxyRuntime != "docker" {
		for _, domain := range proxyDomains(old.Params) {
			if err = r.importCaddyCertificate(domain); err != nil {
				return err
			}
		}
	}
	// Preserve certbot-managed certificates during explicit legacy migration.
	if old.ProxyRuntime != "docker" && old.Params.Proxy == "nginx" {
		for _, domain := range proxyDomains(old.Params) {
			for _, name := range []string{"fullchain.pem", "privkey.pem"} {
				data, e := os.ReadFile(r.systemPath("/etc/letsencrypt/live/" + domain + "/" + name))
				if e != nil {
					return e
				}
				if e = writeFile(filepath.Join(r.Stack, "nginx", "certs", domain, name), data, 0600); e != nil {
					return e
				}
			}
		}
	}
	// Caddy -> Nginx requires importing Caddy's existing PEMs, never silent issuance.
	if proxy == "nginx" {
		for _, domain := range proxyDomains(old.Params) {
			if certificateUsable(filepath.Join(r.Stack, "nginx", "certs", domain), domain) != nil {
				if err = r.importCaddyCertificate(domain); err != nil {
					return fmt.Errorf("cannot safely reuse certificate for %s; no certificate issued; backup: %s: %w", domain, backup, err)
				}
			}
		}
	}
	r.state.Params.Proxy, r.state.ProxyRuntime = proxy, "docker"
	if err = r.writeDockerProxy(true); err == nil {
		err = r.validateDockerProxy(ctx)
	}
	if err != nil {
		r.state = old
		return err
	}
	journal := old
	journal.ProxyRecovery = backup
	journal.ProxyTarget = proxy
	if err = saveState(statePath(r.Root), journal); err != nil {
		r.state = old
		return err
	}
	stopOld := func(c context.Context) error {
		if old.ProxyRuntime == "docker" {
			_, e := r.proxyCompose(c, old.Params.Proxy, "stop")
			return e
		}
		_, e := r.Runner.Run(c, nil, "systemctl", "stop", old.Params.Proxy)
		return e
	}
	if err = stopOld(ctx); err != nil {
		r.state = old
		return err
	}
	rollback := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, stopErr := r.proxyCompose(recovery, proxy, "stop")
		r.state = old
		if old.ProxyRuntime == "docker" {
			if e := copyTree(filepath.Join(backup, "proxy"), r.Stack); e != nil {
				return errors.Join(cause, stopErr, e)
			}
		}
		var startErr error
		if old.ProxyRuntime == "docker" {
			_, startErr = r.proxyCompose(recovery, old.Params.Proxy, "up", "-d")
		} else {
			_, startErr = r.Runner.Run(recovery, nil, "systemctl", "start", old.Params.Proxy)
		}
		if startErr == nil {
			startErr = r.checkPanelProtection(recovery)
		}
		if startErr != nil {
			r.state.ProxyRecovery = backup
			r.state.ProxyTarget = proxy
		}
		return errors.Join(cause, stopErr, startErr, saveState(statePath(r.Root), r.state))
	}
	if err = r.configureDockerProxy(ctx); err == nil {
		err = r.waitHTTPS(ctx)
	}
	if err != nil {
		return rollback(err)
	}
	if err = r.scheduleImportedCertificates(ctx); err != nil {
		return rollback(err)
	}
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return rollback(err)
	}
	return nil
}
