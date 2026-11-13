package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestDomainEnvPreservesUnknownSettings(t *testing.T) {
	p := testParams()
	p.PanelDomain = "new.example.com"
	p.SubscriptionDomain = "new-sub.example.com"
	before := []byte("# retained comment\nPANEL_DOMAIN=old\nFRONT_END_DOMAIN=old\nSUB_PUBLIC_DOMAIN=old\nFUTURE=keep\n")
	data := string(patchDomainEnv(before, p))
	for _, value := range []string{"# retained comment", "FUTURE=keep", "PANEL_DOMAIN=new.example.com", "SUB_PUBLIC_DOMAIN=new-sub.example.com"} {
		if !strings.Contains(data, value) {
			t.Fatal(data)
		}
	}
}

func TestConfigurationBackupIntegrityAndInterruptedRestore(t *testing.T) {
	r, f := managementFixture(t)
	r.state.LayoutVersion = officialLayout
	r.state.ProxyRuntime = "docker"
	if err := r.configure(); err != nil {
		t.Fatal(err)
	}
	if err := r.writeOfficialProxy(); err != nil {
		t.Fatal(err)
	}
	r.state.Params.PanelDomain = "panel.example.test"
	saveState(statePath(r.Root), r.state)
	writeFile(filepath.Join(r.Root, "dns-token.key"), []byte("old-key"), 0600)
	writeFile(filepath.Join(r.Root, "dns-token.enc"), []byte("old-encrypted-token"), 0600)
	backup, err := r.backup(context.Background(), "pre-panel-configuration")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := r.verifyBackup(filepath.Base(backup))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Files["renewal/dns-token.enc"] == "" {
		t.Fatal("renewal snapshot is not integrity protected")
	}
	oldEnv, _ := os.ReadFile(filepath.Join(r.Stack, ".env"))
	writeFile(filepath.Join(r.Stack, ".env"), []byte("failed replacement"), 0600)
	writeFile(filepath.Join(r.Root, "renew-dns.sh"), []byte("new failed wrapper"), 0700)
	writeFile(filepath.Join(r.Root, "dns-token.enc"), []byte("new-encrypted-token"), 0600)
	failure := errors.New("backend unavailable")
	f.response = func(call string, input []byte) ([]byte, error) {
		if strings.Contains(call, " up -d --wait ") {
			return nil, failure
		}
		return nil, nil
	}
	if err = r.restorePanelConfiguration(context.Background(), backup); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	restoredEnv, _ := os.ReadFile(filepath.Join(r.Stack, ".env"))
	if string(restoredEnv) != string(oldEnv) {
		t.Fatal("old environment was not restored")
	}
	if r.state.ConfigurationBackup != backup || r.state.Params.PanelDomain != "panel.example.test" {
		t.Fatal("interrupted recovery lost its journal")
	}
	data, _ := os.ReadFile(filepath.Join(r.Root, "dns-token.enc"))
	if string(data) != "old-encrypted-token" {
		t.Fatal("renewal credentials were not restored")
	}
	if _, err = os.Stat(filepath.Join(r.Root, "renew-dns.sh")); !os.IsNotExist(err) {
		t.Fatal("failed new renewal wrapper remained active")
	}
	data, _ = os.ReadFile(filepath.Join(backup, "renewal", "dns-token.enc"))
	os.WriteFile(filepath.Join(backup, "renewal", "dns-token.enc"), append(data, 'x'), 0600)
	if _, err = r.verifyBackup(filepath.Base(backup)); err == nil {
		t.Fatal("tampered renewal snapshot accepted")
	}
}

func TestInvalidDomainChangeHasNoMutations(t *testing.T) {
	r, f := managementFixture(t)
	r.state.LayoutVersion = officialLayout
	err := r.changePanelConfiguration(context.Background(), services.PanelRequest{Action: "panel-domains", Value: "https://bad.example.com sub.example.com"})
	if err == nil || len(f.calls) != 0 {
		t.Fatal("invalid domain made host changes", err, f.calls)
	}
}

func TestCustomCertificateDisablesOldACMERenewal(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	r.Stack = t.TempDir()
	r.state, _ = newState(testParams())
	cert, key := writeWizardCertificate(t, t.TempDir(), proxyDomains(r.state.Params))
	r.state.Params.Certificate = services.CertificateParameters{Method: "custom", CertificateFile: cert, KeyFile: key}
	writeFile(filepath.Join(r.Root, "acme", r.state.Params.PanelDomain+"_ecc", r.state.Params.PanelDomain+".conf"), []byte("previous renewal"), 0600)
	f := &fakeRunner{}
	r.Runner = f
	if err := r.installPanelCertificates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.Contains(f.calls[0], "--remove") || !strings.Contains(f.calls[0], "-d "+r.state.Params.PanelDomain) {
		t.Fatal("old managed renewal can overwrite custom PEM", f.calls)
	}
}
