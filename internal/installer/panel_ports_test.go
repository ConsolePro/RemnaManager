package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestPanelPortRequirementsMatchSelectedProxy(t *testing.T) {
	for _, proxy := range []string{"nginx", "caddy"} {
		p := testParams()
		p.Proxy = proxy
		required := requiredPanelPorts(p, officialLayout)
		if required[80] != (proxy == "caddy") || required[8443] != (proxy == "nginx") {
			t.Fatal(proxy, required)
		}
		for _, port := range []int{443, 3000, 3001, 3010, 6767} {
			if !required[port] {
				t.Fatal("missing actual bind", proxy, port)
			}
		}
		p.RemoteSubscription = &services.SubscriptionRemote{}
		if requiredPanelPorts(p, officialLayout)[3010] {
			t.Fatal("remote subscription reserved a local application port")
		}
	}
	if !requiredPanelPorts(testParams(), 1)[80] {
		t.Fatal("legacy HTTP-01 must still reserve port 80")
	}
}

func TestNginxPreflightAllowsUnrelatedPort80Service(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	r := NewReal()
	r.Root = t.TempDir()
	r.Stack = t.TempDir()
	r.SystemRoot = t.TempDir()
	os.MkdirAll(filepath.Join(r.SystemRoot, "etc", "nginx"), 0700)
	f := &fakeRunner{response: func(call string, _ []byte) ([]byte, error) {
		if strings.Contains(call, "sshd -T") {
			return []byte("port 22\nport 2222\n"), nil
		}
		if call == "ss -H -ltnp" {
			return []byte("LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:((nginx))\n"), nil
		}
		return nil, nil
	}}
	r.Runner = f
	p := testParams()
	p.PanelDomain = "localhost"
	p.SubscriptionDomain = "localhost"
	if err := r.preflight(context.Background(), p); err != nil {
		t.Fatal("fresh Docker Nginx rejected an unrelated HTTP service", err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "stop") || strings.Contains(call, "systemctl") {
			t.Fatal("touched an existing service", call)
		}
	}
	p.Proxy = "caddy"
	if err := r.preflight(context.Background(), p); err == nil || !strings.Contains(err.Error(), "port 80") {
		t.Fatal("Caddy ignored a real conflict", err)
	}
}

func TestNginxPreflightStillRejects443And8443(t *testing.T) {
	for _, port := range []string{"443", "8443"} {
		r := NewReal()
		r.Runner = &fakeRunner{response: func(string, []byte) ([]byte, error) { return []byte("LISTEN 0 511 [::]:" + port + " [::]:*\n"), nil }}
		err := r.checkPanelPorts(context.Background(), testParams(), officialLayout, false)
		if err == nil || !strings.Contains(err.Error(), "port "+port) {
			t.Fatal("ignored required port", port, err)
		}
	}
}
