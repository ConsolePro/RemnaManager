package installer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in fixtures deploy only with an explicitly named disposable Compose
// project. No Docker socket, firewall or systemd host is required by these tests.
func TestWriteLiveStackFixture(t *testing.T) {
	root := os.Getenv("RM_PANEL_FIXTURE")
	if root == "" {
		t.Skip("opt-in isolated Docker stack")
	}
	if _, err := os.Stat(statePath(filepath.Join(root, "manager"))); !os.IsNotExist(err) {
		t.Fatal("fixture state already exists or is unreadable; use a fresh directory instead of replacing credentials")
	}
	p := testParams()
	p.PanelDomain = "panel.test"
	p.SubscriptionDomain = "sub.test"
	s, e := newState(p)
	if e != nil {
		t.Fatal(e)
	}
	s.BackendTag = "3.4.9"
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.state = s
	if e = r.configure(); e != nil {
		t.Fatal(e)
	}
	if e = saveState(statePath(r.Root), s); e != nil {
		t.Fatal(e)
	}
	data, e := RenderStack(s)
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
	svc := doc["services"].(map[string]any)
	svc["remnawave"].(map[string]any)["ports"] = []string{"127.0.0.1::3000"}
	svc["subscription"].(map[string]any)["ports"] = []string{"127.0.0.1::3010"}
	replace := func(text string) string {
		return strings.ReplaceAll(strings.ReplaceAll(text, "127.0.0.1:3000", "remnawave:3000"), "127.0.0.1:3010", "subscription:3010")
	}
	if e = writeFile(filepath.Join(root, "nginx.conf"), []byte(replace(RenderNginx(s, true))), 0600); e != nil {
		t.Fatal(e)
	}
	caddy := replace(RenderCaddy(s))
	for _, domain := range proxyDomains(p) {
		caddy = strings.ReplaceAll(caddy, domain+" {", domain+" {\n tls internal")
	}
	if e = writeFile(filepath.Join(root, "Caddyfile"), []byte(caddy), 0600); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"nginx", "caddy"} {
		image := "nginx:1.30-alpine"
		vols := []string{"./nginx.conf:/etc/nginx/conf.d/default.conf:ro", "./certs:/etc/letsencrypt/live:ro"}
		if name == "caddy" {
			image = "caddy:2-alpine"
			vols = []string{"./Caddyfile:/etc/caddy/Caddyfile:ro", "caddy-test:/data"}
		}
		svc[name] = map[string]any{"image": image, "networks": []string{"panel"}, "ports": []string{"127.0.0.1::443"}, "volumes": vols, "profiles": []string{"proxy-test"}}
	}
	doc["volumes"].(map[string]any)["caddy-test"] = map[string]any{}
	data, e = json.MarshalIndent(doc, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = writeFile(filepath.Join(root, "compose.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "RemnaManager isolated test"}, DNSNames: []string{p.PanelDomain, p.SubscriptionDomain, "cover.fixture.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	for _, domain := range []string{p.PanelDomain, p.SubscriptionDomain} {
		dir := filepath.Join(root, "certs", domain)
		if e = writeFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
			t.Fatal(e)
		}
		if e = writeFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = writeFile(filepath.Join(root, "firewall.nft"), []byte(FirewallRules([]int{22, 2222}, true)), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestLiveProxyEndpoints(t *testing.T) {
	root, base := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_PANEL_API")
	if root == "" || base == "" || os.Getenv("RM_NGINX_ENDPOINT") == "" {
		t.Skip("opt-in isolated HTTPS proxies")
	}
	s, e := loadState(statePath(filepath.Join(root, "manager")))
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	user, e := apiCall(ctx, client, base, s.Params.PanelDomain, "POST", "/users", s.ManagerToken, map[string]any{"username": "rmtest_" + time.Now().Format("150405"), "expireAt": "2099-12-31T00:00:00.000Z", "trafficLimitBytes": 0, "activeInternalSquads": []string{}})
	if e != nil {
		t.Fatal(e)
	}
	short, e := responseString(user, "shortUuid")
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	for _, path := range []string{filepath.Join(root, "certs", s.Params.PanelDomain, "fullchain.pem"), filepath.Join(root, "caddy-root.crt")} {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if !pool.AppendCertsFromPEM(b) {
			t.Fatal("invalid test CA")
		}
	}
	for _, name := range []string{"NGINX", "CADDY"} {
		endpoint := os.Getenv("RM_" + name + "_ENDPOINT")
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(c context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(c, network, endpoint)
		}}
		proxyClient := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		for _, target := range []string{"https://" + s.Params.PanelDomain + "/api/auth/status", "https://" + s.Params.SubscriptionDomain + "/" + short} {
			req, e := http.NewRequestWithContext(ctx, "GET", target, nil)
			if e != nil {
				t.Fatal(e)
			}
			if req.URL.Host == s.Params.PanelDomain {
				req.AddCookie(&http.Cookie{Name: s.CookieKey, Value: s.CookieValue})
			}
			resp, e := proxyClient.Do(req)
			if e != nil {
				t.Fatalf("%s TLS: %v", name, e)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("%s %s: HTTP %d", name, target, resp.StatusCode)
			}
		}
		transport.CloseIdleConnections()
	}
}

func TestLiveBootstrapAPI(t *testing.T) {
	base, root := os.Getenv("RM_PANEL_API"), os.Getenv("RM_PANEL_FIXTURE")
	if base == "" || root == "" {
		t.Skip("opt-in real backend API")
	}
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.APIBase = base
	s, e := loadState(statePath(r.Root))
	if e != nil {
		t.Fatal(e)
	}
	r.state = s
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if e = r.bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	// Re-running must preserve the registered credentials and the same tokens.
	manager, sub := r.state.ManagerToken, r.state.SubToken
	if e = r.bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	if r.state.ManagerToken != manager || r.state.SubToken != sub {
		t.Fatal("resume changed tokens")
	}
	for _, check := range []struct {
		token   string
		allowed bool
	}{{manager, true}, {sub, false}} {
		req, e := http.NewRequestWithContext(ctx, "GET", base+"/nodes", nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+check.token)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		resp, e := r.Client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		_ = resp.Body.Close()
		if check.allowed && resp.StatusCode != 200 || !check.allowed && resp.StatusCode != 403 {
			t.Fatalf("token permission check failed: HTTP %d", resp.StatusCode)
		}
	}
}
