package installer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Version 2 uses the published Compose structure and names. Older installations
// retain their project/volume identities; a format change must not replace a DB.
const officialLayout = 2

//go:embed templates/*
var officialTemplates embed.FS

func templateFile(name string) string {
	data, err := officialTemplates.ReadFile("templates/" + name)
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func yamlValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// Replace only the image scalar's line; retain comments, anchors, order and
// user settings verbatim instead of serializing a new document over them.
func patchComposeImage(data []byte, service, image string) ([]byte, error) {
	if err := ValidateSubscriptionImage(image); err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	value := yamlValue(yamlValue(yamlValue(&node, "services"), service), "image")
	if value == nil || value.Kind != yaml.ScalarNode {
		return nil, errors.New("Compose service image is missing or not a scalar")
	}
	lines := strings.Split(string(data), "\n")
	i := value.Line - 1
	if i < 0 || i >= len(lines) {
		return nil, errors.New("invalid Compose image position")
	}
	pattern := regexp.MustCompile(`^(\s*image:\s*)(?:'[^']*'|"[^"]*"|[^\s#]+)(.*)$`)
	match := pattern.FindStringSubmatch(lines[i])
	if match == nil {
		return nil, errors.New("inline Compose image edits require a standalone image line")
	}
	lines[i] = match[1] + image + match[2]
	result := []byte(strings.Join(lines, "\n"))
	var check any
	if err := yaml.Unmarshal(result, &check); err != nil {
		return nil, err
	}
	return result, nil
}

func officialService(s State, name string) string {
	if s.LayoutVersion >= officialLayout {
		switch name {
		case "subscription":
			return "remnawave-subscription-page"
		case "nginx":
			return "remnawave-nginx"
		}
	}
	return name
}

func officialArgs(s State, args []string) []string {
	out := append([]string(nil), args...)
	if s.LayoutVersion < officialLayout {
		return out
	}
	command := ""
	skipValue := false
	for i, arg := range out {
		if skipValue {
			skipValue = false
			continue
		}
		switch arg {
		case "--profile", "--entrypoint", "--user", "-u", "--workdir", "-w", "--env", "-e", "--index", "--name", "--volume", "-v", "--publish", "-p", "--pull":
			skipValue = true
			continue
		}
		if arg == "run" || arg == "exec" {
			command = arg
			continue
		}
		out[i] = officialService(s, arg)
		if command != "" {
			switch arg {
			case "subscription", "remnawave-subscription-page", "nginx", "remnawave-nginx", "caddy", "remnawave", "remnawave-db", "remnawave-redis", "tinyauth", "mfa-gateway":
				return out
			}
		}
	}
	return out
}

func renderOfficialStack(s State) ([]byte, error) {
	text := templateFile("panel.compose.yml")
	var err error
	data, err := patchComposeImage([]byte(text), "remnawave", "remnawave/backend:"+s.BackendTag)
	if err != nil {
		return nil, err
	}
	return patchOfficialAuthentication(data, s)
}

func renderOfficialEnv(s State) []byte {
	text := templateFile("panel.env")
	values := map[string]string{"APP_SECRET": s.AppSecret, "METRICS_PASS": s.MetricsPassword, "WEBHOOK_SECRET_HEADER": s.WebhookSecret, "POSTGRES_PASSWORD": s.DBPassword, "DATABASE_URL": "\"postgresql://postgres:" + s.DBPassword + "@remnawave-db:5432/postgres\"", "PANEL_DOMAIN": s.Params.PanelDomain, "FRONT_END_DOMAIN": s.Params.PanelDomain, "SUB_PUBLIC_DOMAIN": s.Params.SubscriptionDomain}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if ok {
			if value, exists := values[key]; exists {
				lines[i] = key + "=" + value
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func renderOfficialProxy(s State) string {
	suffix := "bundled"
	if s.Params.RemoteSubscription != nil {
		suffix = "separate"
	}
	text := templateFile(s.Params.Proxy + "-" + protectionMode(s.Params) + "-" + suffix + ".conf")
	return strings.NewReplacer("REPLACE_WITH_YOUR_DOMAIN", s.Params.PanelDomain, "SUBSCRIPTION_PAGE_DOMAIN", s.Params.SubscriptionDomain, "AUTH_DOMAIN", s.Params.AuthDomain, "ACME_EMAIL", s.Params.Email, "COOKIE_NAME", s.CookieKey, "COOKIE_SECRET", s.CookieValue, "SUBSCRIPTION_API_TOKEN", s.SubToken).Replace(text)
}

func (r *Real) writeOfficialProxy() error {
	proxy := r.state.Params.Proxy
	if proxy != "nginx" && proxy != "caddy" {
		return errors.New("invalid proxy")
	}
	dir := r.Stack + "/" + proxy
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	manifest := templateFile(proxy + ".compose.yml")
	text := renderOfficialProxy(r.state)
	if proxy == "caddy" && commonCertificateUsable(r.Stack, proxyDomains(r.state.Params)) == nil {
		manifest = strings.Replace(manifest, "      - ./config:/config", "      - ./config:/config\n      - ../nginx:/etc/nginx/ssl:ro", 1)
		for _, domain := range proxyDomains(r.state.Params) {
			text = strings.Replace(text, domain+" {\n", domain+" {\n tls /etc/nginx/ssl/fullchain.pem /etc/nginx/ssl/privkey.key\n", 1)
		}
	}
	// Generated only once; normal management and authentication changes preserve
	// the user's Compose edits. Validation still precedes applying configuration.
	manifestPath := dir + "/docker-compose.yml"
	if old, err := os.ReadFile(manifestPath); os.IsNotExist(err) {
		if err = writeFile(manifestPath, []byte(manifest), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if proxy == "caddy" && strings.Contains(manifest, "../nginx:/etc/nginx/ssl:ro") && !strings.Contains(string(old), "../nginx:/etc/nginx/ssl:ro") {
		updated := strings.Replace(string(old), "      - ./config:/config", "      - ./config:/config\n      - ../nginx:/etc/nginx/ssl:ro", 1)
		if updated == string(old) {
			return errors.New("add ../nginx:/etc/nginx/ssl:ro to the Caddy volumes before reusing the certificate")
		}
		if err = writeFile(manifestPath, []byte(updated), 0600); err != nil {
			return err
		}
	}
	name := "nginx.conf"
	if proxy == "caddy" {
		name = "Caddyfile"
		for _, sub := range []string{"data", "config"} {
			if err := os.MkdirAll(dir+"/"+sub, 0700); err != nil {
				return err
			}
		}
	}
	return writeBoundConfig(dir+"/"+name, []byte(text))
}

func commonCertificateUsable(stack string, domains []string) error {
	pair, err := tls.LoadX509KeyPair(stack+"/nginx/fullchain.pem", stack+"/nginx/privkey.key")
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err = certificateUsablePair(cert); err != nil {
		return err
	}
	for _, domain := range domains {
		if err = cert.VerifyHostname(domain); err != nil {
			return err
		}
	}
	return nil
}

func certificateUsablePair(cert *x509.Certificate) error {
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return errors.New("certificate is not valid at the current time")
	}
	return nil
}

func (r *Real) configureOfficialProxy(ctx context.Context) error {
	if err := validateProtectionState(r.state); err != nil {
		return err
	}
	if r.state.Params.Proxy == "nginx" && commonCertificateUsable(r.Stack, proxyDomains(r.state.Params)) != nil {
		if _, err := r.Runner.Run(ctx, panelACMEScript, "bash", "-s", "--", r.Root, r.Stack, r.state.Params.Email, panelCA(r.state.Params), strings.Join(proxyDomains(r.state.Params), ",")); err != nil {
			return err
		}
		if err := commonCertificateUsable(r.Stack, proxyDomains(r.state.Params)); err != nil {
			return err
		}
	} else if r.state.Params.Proxy == "nginx" {
		if err := r.ensureACMERenewal(ctx); err != nil {
			return err
		}
	}
	if err := r.writeOfficialProxy(); err != nil {
		return err
	}
	if err := r.validateDockerProxy(ctx); err != nil {
		return err
	}
	proxy := r.state.Params.Proxy
	if _, err := r.proxyCompose(ctx, proxy, "up", "-d"); err != nil {
		return err
	}
	if proxy == "nginx" {
		_, err := r.proxyCompose(ctx, proxy, "exec", "-T", "nginx", "nginx", "-s", "reload")
		return err
	}
	_, err := r.proxyCompose(ctx, proxy, "exec", "-T", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	return err
}

func (r *Real) checkOfficialResources(ctx context.Context) error {
	for _, volume := range []string{"remnawave-db-data", "valkey-socket"} {
		if _, err := r.Runner.Run(ctx, nil, "docker", "volume", "inspect", volume); err == nil {
			return fmt.Errorf("existing Docker volume %s requires explicit adoption; refusing to attach a fresh installation to it", volume)
		}
	}
	for _, container := range []string{"remnawave", "remnawave-db", "remnawave-redis", "remnawave-subscription-page", "remnawave-nginx", "caddy"} {
		if _, err := r.Runner.Run(ctx, nil, "docker", "container", "inspect", container); err == nil {
			return fmt.Errorf("existing container %s is not owned by this fresh installation", container)
		}
	}
	return nil
}

func patchOfficialAuthentication(data []byte, s State) ([]byte, error) {
	text := string(data)
	begin, end := "  # BEGIN RemnaManager authentication\n", "  # END RemnaManager authentication\n"
	if start := strings.Index(text, begin); start >= 0 {
		finish := strings.Index(text[start:], end)
		if finish < 0 {
			return nil, errors.New("incomplete authentication block")
		}
		text = text[:start] + text[start+finish+len(end):]
	}
	snippet := ""
	switch protectionMode(s.Params) {
	case "tinyauth":
		snippet = templateFile("auth-services.yml")
	case "mfa":
		snippet = strings.ReplaceAll(templateFile("mfa-services.yml"), "REPLACE_WITH_MFA_IMAGE", mfaContainerImage())
	}
	if snippet != "" {
		at := strings.Index(text, "\nnetworks:")
		if at < 0 {
			return nil, errors.New("Compose networks section missing")
		}
		text = text[:at] + "\n" + snippet + text[at:]
	}
	// Keep dormant identity volumes when switching authentication mechanisms.
	volume := ""
	switch protectionMode(s.Params) {
	case "tinyauth":
		volume = "tinyauth-data"
	case "mfa":
		volume = "mfa-data"
	}
	if volume != "" && !strings.Contains(text, "\n  "+volume+":") {
		text += "\n  " + volume + ": {}\n"
	}
	var check any
	if err := yaml.Unmarshal([]byte(text), &check); err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// Single-file bind mounts in the documented Compose files must retain their
// inode when configuration is edited, so reload sees the updated contents.
func writeBoundConfig(path string, data []byte) error {
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return writeFile(path, data, 0600)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("non-regular configuration: %s", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
