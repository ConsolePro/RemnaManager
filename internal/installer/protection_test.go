package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"golang.org/x/crypto/bcrypt"
)

type protectionTransport func(*http.Request) (*http.Response, error)

func TestInstallSummaryUsesProtectedAccessLink(t *testing.T) {
	for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
		p := testParams()
		p.Protection = mode
		if mode == "tinyauth" {
			p.AuthDomain = "auth." + p.PanelDomain
		}
		s, err := newState(p)
		if err != nil {
			t.Fatal(err)
		}
		r := NewReal()
		r.state = s
		text := r.Summary()
		if !strings.Contains(text, "Panel: "+panelAccessURL(s)+"\n") {
			t.Fatal("wrong access URL", mode)
		}
		if mode == "cookie" && !strings.Contains(text, "?"+s.CookieKey+"="+s.CookieValue) {
			t.Fatal("Cookie access token missing")
		}
		if !strings.Contains(text, s.AdminPassword) {
			t.Fatal("initial panel password missing from final instructions")
		}
		if mode == "tinyauth" && !strings.Contains(text, s.TinyPassword) {
			t.Fatal("TinyAuth initial password missing")
		}
		if mode == "mfa" && !strings.Contains(text, s.MFAPassword) {
			t.Fatal("MFA initial password missing")
		}
	}
}

func (f protectionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestProtectionSecretsPersistAndRemainSeparate(t *testing.T) {
	for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
		t.Run(mode, func(t *testing.T) {
			p := testParams()
			p.Protection = mode
			if mode == "tinyauth" {
				p.AuthDomain = "auth." + p.PanelDomain
			}
			s, err := newState(p)
			if err != nil {
				t.Fatal(err)
			}
			if s.TinyPassword == s.AdminPassword || (mode == "cookie" && s.CookieKey == s.CookieValue) {
				t.Fatal("credentials must be independent")
			}
			if mode == "tinyauth" && bcrypt.CompareHashAndPassword([]byte(s.TinyHash), []byte(s.TinyPassword)) != nil {
				t.Fatal("TinyAuth hash does not match saved password")
			}
			if mode == "mfa" && (s.MFAPassword == "" || s.MFAPassword == s.AdminPassword || s.MFASigningKey == "" || s.MFASigningKey == s.AppSecret) {
				t.Fatal("MFA credentials must be independent")
			}
			path := statePath(t.TempDir())
			if err = saveState(path, s); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadState(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = ensureProtection(&loaded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s, loaded) {
				t.Fatal("resume replaced protection credentials")
			}
			for _, config := range []string{RenderCaddy(loaded), RenderNginx(loaded, true)} {
				if mode == "mfa" && (strings.Contains(config, s.MFAPassword) || strings.Contains(config, s.MFASigningKey) || !strings.Contains(config, "127.0.0.1:3030")) {
					t.Fatal("MFA front proxy must forward to private gateway without its secrets")
				}
				if mode == "tinyauth" && (strings.Contains(config, s.TinyPassword) || strings.Contains(config, s.TinyHash)) {
					t.Fatal("TinyAuth secrets included in public proxy configuration")
				}
			}
			if mode == "mfa" {
				if !strings.Contains(RenderMFAGateway(s), "require mfa") || strings.Contains(RenderMFAGateway(s), s.MFASigningKey) {
					t.Fatal("MFA gateway must require a factor and read key from environment")
				}
				data, _ := RenderStack(s)
				var doc map[string]any
				json.Unmarshal(data, &doc)
				auth := doc["services"].(map[string]any)["mfa-gateway"].(map[string]any)
				if auth["ports"].([]any)[0] != "127.0.0.1:3030:8080" {
					t.Fatal("MFA gateway exposed publicly")
				}
			}
			if mode == "tinyauth" {
				env := string(renderTinyEnv(s))
				if strings.Contains(env, s.TinyPassword) || !strings.Contains(env, "TINYAUTH_AUTH_USERS='"+p.Admin+":"+s.TinyHash+"'") {
					t.Fatal("bcrypt must be quoted literally, plain password must not enter the container")
				}
				data, _ := RenderStack(s)
				var doc map[string]any
				json.Unmarshal(data, &doc)
				auth := doc["services"].(map[string]any)["tinyauth"].(map[string]any)
				if auth["ports"].([]any)[0] != "127.0.0.1:3020:3000" {
					t.Fatal("TinyAuth port is exposed")
				}
			}
		})
	}
}

func TestTinyAuthDomainMustShareOnlyPanelCookieScope(t *testing.T) {
	p := testParams()
	p.Protection = "tinyauth"
	for _, domain := range []string{"auth.panel.example.com", "login.panel.example.com"} {
		p.AuthDomain = domain
		if ValidateProtection(p) != nil {
			t.Fatal(domain)
		}
	}
	for _, domain := range []string{"", "panel.example.com", "auth.other.example.com", "nested.auth.panel.example.com", "auth.panel.example.com.attacker.test", "x;return 200;"} {
		p.AuthDomain = domain
		if ValidateProtection(p) == nil {
			t.Fatal("unsafe cookie scope accepted", domain)
		}
	}
}

func TestTinyAuthDoesNotConflictWithSSH(t *testing.T) {
	p := testParams()
	p.SSHPorts = []int{3020}
	if err := ValidateParameters(p); err != nil {
		t.Fatal("Cookie mode should leave port 3020 available for SSH", err)
	}
	p.Protection = "tinyauth"
	p.AuthDomain = "auth." + p.PanelDomain
	if ValidateParameters(p) == nil {
		t.Fatal("TinyAuth port conflicts with SSH")
	}
}

func TestMissingSecretsNeverRenderOpenPanel(t *testing.T) {
	s := State{Params: testParams()}
	nginx, caddy := RenderNginx(s, true), RenderCaddy(s)
	if strings.Contains(nginx, "127.0.0.1:3000") || strings.Contains(caddy, "127.0.0.1:3000") {
		t.Fatal("missing gate secrets exposed panel")
	}
	if !strings.Contains(nginx, "return 403") || !strings.Contains(caddy, "respond Forbidden 403") {
		t.Fatal("missing secrets must fail closed")
	}
}

func TestEnableProtectionRetriesAfterProxyFailure(t *testing.T) {
	r, f := managementFixture(t)
	r.SystemRoot = t.TempDir()
	r.state.Params.Proxy = "caddy"
	r.state.CookieKey, r.state.CookieValue = "", "" // An older Go installation.
	if err := saveState(statePath(r.Root), r.state); err != nil {
		t.Fatal(err)
	}
	f.response = func(call string, _ []byte) ([]byte, error) {
		if strings.HasPrefix(call, "caddy validate") {
			return nil, errors.New("test validation failure")
		}
		return nil, nil
	}
	request := services.PanelRequest{Action: "panel-protection", Protection: "cookie"}
	if _, err := r.manage(context.Background(), request); err == nil {
		t.Fatal("proxy failure ignored")
	}
	failed, err := loadState(statePath(r.Root))
	if err != nil || !failed.ProtectionPending || failed.CookieKey == "" {
		t.Fatal("pending credentials not persisted", err)
	}
	if _, err = r.manage(context.Background(), services.PanelRequest{Action: "panel-start"}); err == nil {
		t.Fatal("pending protection did not block unrelated mutation")
	}
	f.response = nil
	r.Client = &http.Client{Transport: protectionTransport(func(req *http.Request) (*http.Response, error) {
		status := 403
		if cookie, e := req.Cookie(failed.CookieKey); e == nil && cookie.Value == failed.CookieValue {
			status = 200
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture"))}, nil
	})}
	result, err := r.manage(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(statePath(r.Root))
	if err != nil || loaded.ProtectionPending || loaded.CookieKey != failed.CookieKey || loaded.CookieValue != failed.CookieValue || loaded.AdminPassword != failed.AdminPassword {
		t.Fatal("retry changed credentials or did not finish", err)
	}
	if !strings.Contains(result.Text, panelAccessURL(loaded)) {
		t.Fatal("private URL missing from result")
	}
	config, err := os.ReadFile(r.systemPath("/etc/caddy/Caddyfile"))
	if err != nil || !strings.Contains(string(config), loaded.CookieKey) {
		t.Fatal("proxy did not receive saved gate", err)
	}
	if r.Installed() != true {
		t.Fatal("completed protection kept installer blocked")
	}
}

func TestProtectionSecretsAreRedacted(t *testing.T) {
	r := NewReal()
	p := testParams()
	p.Protection = "tinyauth"
	p.AuthDomain = "auth." + p.PanelDomain
	r.state, _ = newState(p)
	r.state.CookieKey = "cookie-key-fixture"
	r.state.CookieValue = "cookie-value-fixture"
	r.state.MFAPassword = "mfa-password-fixture"
	r.state.MFASigningKey = "mfa-signing-key-fixture"
	text := r.state.TinyPassword + " " + r.state.TinyHash + " " + r.state.CookieKey + " " + r.state.CookieValue + " " + r.state.MFAPassword + " " + r.state.MFASigningKey
	if strings.Contains(r.safeOutput([]byte(text)), "fixture") || strings.Contains(r.safeOutput([]byte(text)), r.state.TinyPassword) {
		t.Fatal("protection secrets leaked to logs")
	}
}
