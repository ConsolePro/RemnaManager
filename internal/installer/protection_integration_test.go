package installer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in fixture: only synthetic backends and test credentials, no host systemd,
// firewall, panel database or production Compose project.
func TestWriteProtectionFixture(t *testing.T) {
	root := os.Getenv("RM_PROTECTION_FIXTURE")
	if root == "" {
		t.Skip("opt-in isolated proxy protection fixture")
	}
	if _, err := os.Stat(filepath.Join(root, "compose.json")); !os.IsNotExist(err) {
		t.Fatal("use a fresh fixture directory")
	}
	p := testParams()
	p.PanelDomain, p.SubscriptionDomain = "panel.example.test", "sub.example.test"
	p.Protection = "cookie"
	if os.Getenv("RM_PROTECTION_REMOTE") == "1" {
		p.RemoteSubscription = &services.SubscriptionRemote{User: "root", Proxy: "caddy"}
	}
	s, err := newState(p)
	if err != nil {
		t.Fatal(err)
	}
	s.SubToken = "fixture-subscription-token"
	if os.Getenv("RM_PROTECTION_OFFICIAL") == "1" {
		s.LayoutVersion = officialLayout
	}
	if err = saveState(statePath(root), s); err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "RemnaManager protection test"}, DNSNames: []string{p.PanelDomain, p.SubscriptionDomain, "auth." + p.PanelDomain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := x509.MarshalECPrivateKey(key)
	for name, data := range map[string][]byte{"cert.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "key.pem": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})} {
		if err = writeFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	backend := `from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
import threading
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  if self.path.startswith('/api/nodes') and self.headers.get('Authorization') != 'Bearer fixture-panel-token':
   self.send_response(401); self.end_headers(); return
  if self.headers.get('X-Api-Key'):
   self.send_response(400); self.end_headers(); return
  self.send_response(200); self.end_headers(); self.wfile.write(b'fixture backend reached')
 def log_message(self,*args): pass
for port in [3000,3010]:
 threading.Thread(target=ThreadingHTTPServer(('0.0.0.0',port),Handler).serve_forever,daemon=True).start()
threading.Event().wait()
`
	if err = writeFile(filepath.Join(root, "backend.py"), []byte(backend), 0600); err != nil {
		t.Fatal(err)
	}
	services := map[string]any{"backend": map[string]any{"image": "python:3.13-alpine", "command": []string{"python", "/backend.py"}, "volumes": []string{"./backend.py:/backend.py:ro"}}}
	for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
		s.Params.Protection = mode
		s.Params.AuthDomain = ""
		if mode == "tinyauth" {
			s.Params.AuthDomain = "auth." + p.PanelDomain
			if err = ensureProtection(&s); err != nil {
				t.Fatal(err)
			}
			if err = saveState(filepath.Join(root, "tiny-state.json"), s); err != nil {
				t.Fatal(err)
			}
			if err = writeFile(tinyEnvPath(root), renderTinyEnv(s), 0600); err != nil {
				t.Fatal(err)
			}
			services["tinyauth"] = map[string]any{"image": tinyAuthImage, "env_file": []string{"tinyauth.env"}, "volumes": []string{"auth-data:/data"}, "healthcheck": map[string]any{"test": []string{"CMD", "tinyauth", "healthcheck"}, "interval": "2s", "retries": 30}}
		}
		if mode == "mfa" {
			if err = writeFile(filepath.Join(root, "mfa-build", "Dockerfile"), []byte(mfaDockerfile), 0600); err != nil {
				t.Fatal(err)
			}
			if err = ensureProtection(&s); err != nil {
				t.Fatal(err)
			}
			if err = saveState(filepath.Join(root, "mfa-state.json"), s); err != nil {
				t.Fatal(err)
			}
			if err = writeFile(filepath.Join(root, "mfa.env"), renderMFAEnv(s), 0600); err != nil {
				t.Fatal(err)
			}
			config := strings.ReplaceAll(RenderMFAGateway(s), "remnawave:3000", "backend:3000")
			if err = writeFile(filepath.Join(root, "mfa.Caddyfile"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			services["mfa-gateway"] = map[string]any{"image": mfaImage, "env_file": []string{"mfa.env"}, "volumes": []string{"./mfa.Caddyfile:/etc/caddy/Caddyfile:ro", "mfa-data:/data"}, "healthcheck": map[string]any{"test": []string{"CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/_rm_gateway_health"}, "interval": "2s", "retries": 30}}
		}
		replace := func(text string) string {
			for from, to := range map[string]string{"127.0.0.1:3000": "backend:3000", "127.0.0.1:3010": "backend:3010", "127.0.0.1:3020": "tinyauth:3000", "127.0.0.1:3030": "mfa-gateway:8080", "remnawave:3000": "backend:3000", "subscription:3010": "backend:3010", "remnawave-subscription-page:3010": "backend:3010"} {
				text = strings.ReplaceAll(text, from, to)
			}
			return text
		}
		for _, proxy := range []string{"nginx", "caddy"} {
			name := proxy + "-" + mode
			s.Params.Proxy = proxy
			config := replace(dockerProxyConfig(s, true))
			image := "nginx:1.30-alpine"
			if s.LayoutVersion >= officialLayout {
				image = "nginx:1.30"
			}
			file, target := name+".conf", "/etc/nginx/conf.d/default.conf"
			config = strings.ReplaceAll(config, "/etc/nginx/ssl/fullchain.pem", "/certs/cert.pem")
			config = strings.ReplaceAll(config, "/etc/nginx/ssl/privkey.key", "/certs/key.pem")
			for _, domain := range proxyDomains(s.Params) {
				config = strings.ReplaceAll(config, "/etc/nginx/certs/"+domain+"/fullchain.pem", "/certs/cert.pem")
				config = strings.ReplaceAll(config, "/etc/nginx/certs/"+domain+"/privkey.pem", "/certs/key.pem")
			}
			if proxy == "caddy" {
				image, file, target = "caddy:2-alpine", name+".caddy", "/etc/caddy/Caddyfile"
				if s.LayoutVersion >= officialLayout {
					image = "caddy:2.9"
				}
				config = replace(dockerProxyConfig(s, true))
				for _, domain := range proxyDomains(s.Params) {
					config = strings.ReplaceAll(config, domain+" {", domain+" {\n tls /certs/cert.pem /certs/key.pem")
				}
			}
			if err = writeFile(filepath.Join(root, file), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			services[name] = map[string]any{"image": image, "ports": []string{"127.0.0.1::443"}, "volumes": []string{"./" + file + ":" + target + ":ro", "./cert.pem:/certs/cert.pem:ro", "./key.pem:/certs/key.pem:ro"}, "depends_on": []string{"backend", "tinyauth", "mfa-gateway"}}
		}
	}
	doc := map[string]any{"services": services, "volumes": map[string]any{"auth-data": map[string]any{}, "mfa-data": map[string]any{}}}
	data, _ := json.MarshalIndent(doc, "", "  ")
	if err = writeFile(filepath.Join(root, "compose.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLivePanelProtection(t *testing.T) {
	root := os.Getenv("RM_PROTECTION_FIXTURE")
	if root == "" || os.Getenv("RM_PROTECTION_NGINX_COOKIE") == "" {
		t.Skip("opt-in isolated Nginx/Caddy/TinyAuth endpoints")
	}
	s, err := loadState(statePath(root))
	if err != nil {
		t.Fatal(err)
	}
	tiny, err := loadState(filepath.Join(root, "tiny-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	mfa, err := loadState(filepath.Join(root, "mfa-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(filepath.Join(root, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	mfaSecret := ""
	if saved, err := os.ReadFile(filepath.Join(root, "fixture-totp-secret")); err == nil {
		mfaSecret = string(saved)
	}
	for _, proxy := range []string{"nginx", "caddy"} {
		for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
			t.Run(proxy+"/"+mode, func(t *testing.T) {
				endpoint := os.Getenv("RM_PROTECTION_" + strings.ToUpper(proxy+"_"+mode))
				if endpoint == "" {
					t.Fatal("missing endpoint")
				}
				transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, endpoint)
				}}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				get := func(path, cookie string) *http.Response {
					req, _ := http.NewRequest("GET", "https://"+s.Params.PanelDomain+path, nil)
					req.Header.Set("User-Agent", "Mozilla/5.0 Gecko/20100101 Firefox/140.0 (RemnaManager test)")
					req.Header.Set("Authorization", "Bearer cannot-bypass-protection")
					req.Header.Set("Remote-User", "admin")
					req.Header.Set("X-Original-URL", "https://attacker.invalid/")
					req.Header.Set("X-Forwarded-Host", "attacker.invalid")
					if cookie != "" {
						req.Header.Set("Cookie", cookie)
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					return resp
				}
				for _, path := range []string{"/", "/auth/login", "/api/auth/status", "/oauth2/callback/fake?code=x&state=x", "/api/../auth/login"} {
					resp := get(path, "")
					resp.Body.Close()
					if mode == "cookie" && resp.StatusCode != 403 {
						t.Fatalf("anonymous %s: %d", path, resp.StatusCode)
					}
					if mode == "tinyauth" {
						location, _ := url.Parse(resp.Header.Get("Location"))
						if resp.StatusCode != 302 || location.Host != tiny.Params.AuthDomain {
							t.Fatalf("TinyAuth redirect %s: %d %s", path, resp.StatusCode, resp.Header.Get("Location"))
						}
					}
					if mode == "mfa" {
						location, _ := url.Parse(resp.Header.Get("Location"))
						if resp.StatusCode < 300 || resp.StatusCode >= 400 || location.Host != mfa.Params.PanelDomain || !strings.HasPrefix(location.Path, "/r") {
							t.Fatalf("MFA redirect %s: %d %s", path, resp.StatusCode, resp.Header.Get("Location"))
						}
					}
				}
				if mode == "cookie" {
					for _, cookie := range []string{s.CookieKey + "=wrong", "other=" + s.CookieValue, s.CookieKey + "=" + s.CookieValue + "extra"} {
						resp := get("/api/auth/status", cookie)
						resp.Body.Close()
						if resp.StatusCode != 403 {
							t.Fatal("invalid cookie passed gate")
						}
					}
					resp := get("/auth/login?"+s.CookieKey+"=wrong", "")
					resp.Body.Close()
					if resp.StatusCode != 403 {
						t.Fatal("wrong query passed gate")
					}
					resp = get("/auth/login?"+s.CookieKey+"="+s.CookieValue, "")
					resp.Body.Close()
					cookies := resp.Cookies()
					location, _ := url.Parse(resp.Header.Get("Location"))
					if resp.StatusCode != 303 || location.Path != "/auth/login" || location.RawQuery != "" || (location.Host != "" && location.Host != s.Params.PanelDomain) || len(cookies) != 1 || cookies[0].Name != s.CookieKey || cookies[0].Value != s.CookieValue || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
						t.Fatalf("secret link: status=%d location=%q cookies=%d", resp.StatusCode, resp.Header.Get("Location"), len(cookies))
					}
					resp = get("/api/auth/status", "unrelated=x; "+s.CookieKey+"="+s.CookieValue)
					resp.Body.Close()
					if resp.StatusCode != 200 {
						t.Fatal("valid cookie did not reach backend", resp.StatusCode)
					}
				} else if mode == "tinyauth" {
					jar, _ := cookiejar.New(nil)
					client.Jar = jar
					for _, password := range []string{"incorrect", tiny.TinyPassword} {
						body, _ := json.Marshal(map[string]string{"username": tiny.Params.Admin, "password": password})
						req, _ := http.NewRequest("POST", "https://"+tiny.Params.AuthDomain+"/api/user/login", strings.NewReader(string(body)))
						req.Header.Set("Content-Type", "application/json")
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						want := 401
						if password == tiny.TinyPassword {
							want = 200
						}
						if resp.StatusCode != want {
							t.Fatalf("TinyAuth login expected %d got %d", want, resp.StatusCode)
						}
					}
					resp := get("/auth/login", "")
					defer resp.Body.Close()
					body, _ := io.ReadAll(resp.Body)
					if resp.StatusCode != 200 || !strings.Contains(string(body), "fixture backend reached") {
						t.Fatal("authenticated TinyAuth session did not reach panel login", resp.StatusCode)
					}
					client.Jar = nil
				}
				if mode == "cookie" || mode == "tinyauth" {
					for _, validGate := range []bool{false, true} {
						for _, validBearer := range []bool{false, true} {
							req, _ := http.NewRequest("GET", "https://"+s.Params.PanelDomain+"/api/nodes", nil)
							if validBearer {
								req.Header.Set("Authorization", "Bearer fixture-panel-token")
							} else {
								req.Header.Set("Authorization", "Bearer incorrect")
							}
							if mode == "cookie" {
								value := "wrong"
								if validGate {
									value = s.CookieValue
								}
								req.AddCookie(&http.Cookie{Name: s.CookieKey, Value: value})
							} else {
								password := "wrong"
								if validGate {
									password = tiny.TinyPassword
								}
								req.Header.Set("X-Api-Key", "Basic "+base64.StdEncoding.EncodeToString([]byte(tiny.Params.Admin+":"+password)))
							}
							resp, err := client.Do(req)
							if err != nil {
								t.Fatal(err)
							}
							resp.Body.Close()
							if (resp.StatusCode == 200) != (validGate && validBearer) {
								t.Fatalf("API needs both credentials: gate=%v bearer=%v status=%d", validGate, validBearer, resp.StatusCode)
							}
						}
					}
				}
				if mode == "mfa" {
					mfaSecret = exerciseMFAPortal(t, client, mfa, mfaSecret)
					if err := writeFile(filepath.Join(root, "fixture-totp-secret"), []byte(mfaSecret), 0600); err != nil {
						t.Fatal(err)
					}
				}
				resp, err := client.Get("https://" + s.Params.SubscriptionDomain + "/subscription-fixture")
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatal("public subscription blocked")
				}
				r := NewReal()
				r.state = s
				if mode == "tinyauth" {
					r.state = tiny
				}
				if mode == "mfa" {
					r.state = mfa
				}
				r.Client = client
				if err = r.checkPanelProtection(context.Background()); err != nil {
					t.Fatal("installer verification failed", err)
				}
				fmt.Printf("%s/%s: anonymous denied, credentials verified, subscription public\n", proxy, mode)
			})
		}
	}
}
