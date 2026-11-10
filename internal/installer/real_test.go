package installer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func testParams() services.PanelParameters {
	return services.PanelParameters{PanelDomain: "panel.example.com", SubscriptionDomain: "sub.example.com", Proxy: "nginx", Email: "admin@example.com", Admin: "admin", SSHPorts: []int{22, 2222}}
}

type fakeRunner struct {
	calls          []string
	nft, failApply bool
	response       func(string, []byte) ([]byte, error)
}

func (f *fakeRunner) Run(_ context.Context, in []byte, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.response != nil {
		return f.response(call, in)
	}
	switch {
	case strings.Contains(call, "sshd -T"):
		return []byte("port 22\nport 2222\n"), nil
	case strings.HasPrefix(call, "ufw "), strings.Contains(call, "--quiet firewalld"):
		return nil, errors.New("not active")
	case strings.Contains(call, "legacy"):
		return nil, errors.New("not installed")
	case call == "nft -j list ruleset":
		return []byte(`{"nftables":[]}`), nil
	case strings.Contains(call, "nft list table"):
		if !f.nft {
			return nil, errors.New("no table")
		}
		return []byte(FirewallRules([]int{22, 2222}, false)), nil
	case call == "nft -f -":
		if f.failApply {
			return nil, errors.New("rejected")
		}
		f.nft = true
	case strings.Contains(call, "systemctl is-active"):
		return []byte("active\n"), nil
	}
	return nil, nil
}

func TestPrivateStateAndComposeIsolation(t *testing.T) {
	p := testParams()
	s, e := newState(p)
	if e != nil {
		t.Fatal(e)
	}
	s.BackendTag = "3.4.9"
	path := filepath.Join(t.TempDir(), "panel.json")
	if e = saveState(path, s); e != nil {
		t.Fatal(e)
	}
	loaded, e := loadState(path)
	if e != nil || !reflect.DeepEqual(s, loaded) {
		t.Fatal(e)
	}
	data, e := RenderStack(s)
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Services map[string]struct {
			Ports []string
			Image string
		}
	}
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
	for name, service := range doc.Services {
		for _, port := range service.Ports {
			if !strings.HasPrefix(port, "127.0.0.1:") {
				t.Fatal("public application port", name, port)
			}
		}
	}
	if len(doc.Services["remnawave-db"].Ports) != 0 || len(doc.Services["remnawave-redis"].Ports) != 0 {
		t.Fatal("database/cache exposed")
	}
	if strings.Contains(string(data), "network_mode") || strings.Contains(string(data), "container_name") {
		t.Fatal("stack must remain isolated")
	}
	if s.DBPassword == s.AppSecret || len(s.WebhookSecret) != 64 {
		t.Fatal("invalid random secrets")
	}
}

func TestFirewallRollbackBeforeApplyAndCommit(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	r.SystemRoot = t.TempDir()
	f := &fakeRunner{}
	r.Runner = f
	r.state, _ = newState(testParams())
	t.Setenv("SSH_CONNECTION", "192.0.2.1 50000 192.0.2.2 2222")
	if e := r.applyFirewall(context.Background()); e != nil {
		t.Fatal(e)
	}
	arm, apply := -1, -1
	for i, call := range f.calls {
		if strings.HasPrefix(call, "systemd-run") {
			arm = i
		}
		if call == "nft -f -" {
			apply = i
		}
	}
	if arm < 0 || apply <= arm || !r.state.FirewallPending || r.state.Ready {
		t.Fatal("firewall applied without external rollback", f.calls)
	}
	if e := r.Confirm(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !r.state.Ready || r.state.FirewallPending {
		t.Fatal("confirmation not persisted")
	}
	if _, e := os.Stat(r.systemPath("/etc/systemd/system/remnamanager-firewall.service")); e != nil {
		t.Fatal(e)
	}
	r.state.Ready = false
	r.state.FirewallPending = true
	r.state.RollbackAt = time.Now().Add(-time.Second)
	if r.Confirm(context.Background()) == nil {
		t.Fatal("expired confirmation accepted")
	}
}

func TestFirewallApplyFailureKeepsRollbackArmed(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	f := &fakeRunner{failApply: true}
	r.Runner = f
	r.state, _ = newState(testParams())
	if r.applyFirewall(context.Background()) == nil {
		t.Fatal("expected failure")
	}
	if !r.state.FirewallPending || r.state.Ready {
		t.Fatal("failed apply disarmed rollback")
	}
}
func TestForeignFirewallRefused(t *testing.T) {
	for _, data := range []string{
		`{"nftables":[{"chain":{"family":"inet","table":"filter","name":"input","hook":"input","policy":"drop"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"INPUT","hook":"input","policy":"accept"}},{"rule":{"family":"ip","table":"filter","chain":"INPUT"}}]}`,
	} {
		if CheckForeignFirewall([]byte(data)) == nil {
			t.Fatal("foreign firewall accepted")
		}
	}
	rules := FirewallRules([]int{22, 2222}, true)
	for _, fragment := range []string{"policy drop", "2222", "ct state established,related accept", "iifname \"lo\" accept", "1, 58", "udp dport 443 accept", "ct status dnat drop"} {
		if !strings.Contains(rules, fragment) {
			t.Fatal(fragment)
		}
	}
	if strings.Contains(rules, "flush ruleset") {
		t.Fatal("global firewall flush")
	}
}

func TestUnownedTableRefusedBeforeArmingRollback(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	f := &fakeRunner{nft: true}
	r.Runner = f
	r.state, _ = newState(testParams())
	if err := r.applyFirewall(context.Background()); err == nil {
		t.Fatal("unowned table was overwritten")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "systemd-run") || call == "nft -f -" {
			t.Fatal("mutated unowned firewall", call)
		}
	}
}

func TestHTTPBootstrapNeverProxiesCredentials(t *testing.T) {
	config := RenderNginx(testParams(), false)
	if strings.Contains(config, "proxy_pass") || !strings.Contains(config, "return 503") || !strings.Contains(config, "/.well-known/acme-challenge/") {
		t.Fatal("HTTP bootstrap must serve only ACME")
	}
}

func TestRevokedSavedTokensStopResume(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/auth/status" {
			_, _ = w.Write([]byte(`{"response":{"isRegisterAllowed":false}}`))
			return
		}
		if req.Method != "GET" {
			t.Error("resume attempted to replace saved credentials")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	r := NewReal()
	r.APIBase = server.URL + "/api"
	r.Client = server.Client()
	r.state, _ = newState(testParams())
	r.state.ManagerToken, r.state.SubToken = "revoked-manager", "revoked-sub"
	if err := r.bootstrap(context.Background()); err == nil {
		t.Fatal("revoked credentials accepted")
	}
}
func TestSSHAndParameterValidation(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "192.0.2.1 5000 192.0.2.2 2200")
	ports, e := DetectSSHPorts(context.Background(), &fakeRunner{})
	if e != nil || !reflect.DeepEqual(ports, []int{22, 2200, 2222}) {
		t.Fatal(ports, e)
	}
	for _, mutate := range []func(*services.PanelParameters){func(p *services.PanelParameters) { p.Proxy = "nginx;id" }, func(p *services.PanelParameters) { p.SSHPorts = []int{443} }, func(p *services.PanelParameters) { p.PanelDomain = "127.0.0.1" }, func(p *services.PanelParameters) { p.Admin = "foo\nbar" }} {
		p := testParams()
		mutate(&p)
		if ValidateParameters(p) == nil {
			t.Fatal(p)
		}
	}
}

func TestBootstrapPersistsTokensAndResumes(t *testing.T) {
	calls := 0
	register := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Forwarded-Proto") != "https" {
			t.Error("missing trusted proxy headers")
		}
		switch req.URL.Path {
		case "/api/nodes", "/api/system/stats":
			_, _ = w.Write([]byte(`{"response":{}}`))
		case "/api/auth/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]bool{"isRegisterAllowed": register}})
		case "/api/auth/register":
			register = false
			_, _ = w.Write([]byte(`{"response":{"accessToken":"jwt-test"}}`))
		case "/api/tokens":
			calls++
			if req.Header.Get("Authorization") != "Bearer jwt-test" {
				t.Error("missing session JWT")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]string{"token": "api-key-" + strings.Repeat("x", calls)}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	r := NewReal()
	r.Root = t.TempDir()
	r.Stack = t.TempDir()
	r.APIBase = server.URL + "/api"
	r.Client = server.Client()
	r.state, _ = newState(testParams())
	if e := r.bootstrap(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls != 2 || r.state.ManagerToken == r.state.SubToken {
		t.Fatal("tokens must be separate", calls)
	}
	if e := r.bootstrap(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("resume recreated API tokens")
	}
	s, e := loadState(statePath(r.Root))
	if e != nil || s.SubToken == "" {
		t.Fatal(e)
	}
}
