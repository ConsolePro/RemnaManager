package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func managerFixture(t *testing.T) (*Manager, *[]byte, *Release) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "remanager")
	if err := os.WriteFile(target, []byte("old-manager"), 0755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("new-manager")
	release := Release{Version: "v1.2.0", Assets: []Asset{{Name: "remanager-linux-amd64", URL: "https://github.com/ConsolePro/RemnaManager/releases/download/v1.2.0/remanager-linux-amd64", Digest: "sha256:" + binaryHash(payload), Size: int64(len(payload))}}}
	client := &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
		var data []byte
		if req.URL.Host == "api.github.com" {
			data, _ = json.Marshal(release)
		} else if req.URL.Host == "github.com" {
			data = payload
		} else {
			t.Fatal("unexpected host", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})}
	m := &Manager{Catalog: Catalog{Client: client, Repo: "ConsolePro/RemnaManager"}, Target: target, Arch: "amd64"}
	m.ReadVersion = func(_ context.Context, path string) (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if string(data) == "old-manager" {
			return "v1.0.0", nil
		}
		return "v1.2.0", nil
	}
	m.Validate = func(ctx context.Context, path, version string) error {
		got, err := m.version(ctx, path)
		if err != nil {
			return err
		}
		if got != version {
			return errors.New("wrong fixture version")
		}
		return nil
	}
	return m, &payload, &release
}

func TestManagerUpdateAndOfflineRollback(t *testing.T) {
	m, _, _ := managerFixture(t)
	ctx := context.Background()
	plan, err := m.Prepare(ctx, "latest", false)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.Target)
	if string(before) != "old-manager" {
		t.Fatal("prepare changed the executable")
	}
	if err = m.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(m.Target)
	backup, _ := os.ReadFile(m.Target + ".previous")
	if string(installed) != "new-manager" || string(backup) != "old-manager" {
		t.Fatal("update did not preserve previous executable")
	}
	m.Catalog.Client = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("rollback must work without network")
		return nil, nil
	})}
	plan, err = m.Prepare(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	installed, _ = os.ReadFile(m.Target)
	if string(installed) != "old-manager" {
		t.Fatal("rollback did not restore previous executable")
	}
}
func TestManagerRejectsTamperingAndConcurrentChanges(t *testing.T) {
	for _, scenario := range []string{"download-corrupt", "release-changed", "target-changed", "cancelled", "candidate-invalid"} {
		t.Run(scenario, func(t *testing.T) {
			m, payload, release := managerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan, err := m.Prepare(ctx, "", false)
			if err != nil {
				t.Fatal(err)
			}
			expected := "old-manager"
			switch scenario {
			case "download-corrupt":
				*payload = []byte("tampered")
			case "release-changed":
				release.Assets[0].Digest = "sha256:" + strings.Repeat("b", 64)
			case "target-changed":
				expected = "external-change"
				if err := os.WriteFile(m.Target, []byte(expected), 0755); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "candidate-invalid":
				m.Validate = func(context.Context, string, string) error { return errors.New("wrong executable") }
			}
			if err = m.Apply(ctx, plan); err == nil {
				t.Fatal("unsafe update succeeded")
			}
			installed, _ := os.ReadFile(m.Target)
			if string(installed) != expected {
				t.Fatal("failed update replaced the original")
			}
		})
	}
}
func TestManagerFailedStartupRestoresPreviousExecutable(t *testing.T) {
	m, _, _ := managerFixture(t)
	ctx := context.Background()
	plan, err := m.Prepare(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	validate := m.Validate
	m.Validate = func(ctx context.Context, path, version string) error {
		if path == m.Target && version == "v1.2.0" {
			return errors.New("startup failed")
		}
		return validate(ctx, path, version)
	}
	if err = m.Apply(ctx, plan); err == nil {
		t.Fatal("failed startup accepted")
	}
	installed, _ := os.ReadFile(m.Target)
	if string(installed) != "old-manager" {
		t.Fatal("previous executable not restored")
	}
}
func TestManagerRecoversInterruptedReplacement(t *testing.T) {
	m, _, _ := managerFixture(t)
	old := []byte("old-manager")
	next := []byte("new-manager")
	for path, data := range map[string][]byte{m.Target: next, m.Target + ".previous": old} {
		if err := os.WriteFile(path, data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	journal := updateJournal{Previous: savedBinary{Version: "v1.0.0", SHA256: binaryHash(old)}, Next: savedBinary{Version: "v1.2.0", SHA256: binaryHash(next)}}
	if err := writeJSON(m.Target+".update.json", journal); err != nil {
		t.Fatal(err)
	}
	m.Validate = func(_ context.Context, path, version string) error {
		if version == "v1.2.0" {
			return errors.New("broken startup")
		}
		return nil
	}
	if err := m.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(m.Target)
	if string(installed) != "old-manager" {
		t.Fatal("recovery did not restore backup")
	}
}
func TestManagerLockAndNonELFRejection(t *testing.T) {
	m, _, _ := managerFixture(t)
	unlock, err := managerLock(m.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), "", false); err == nil {
		t.Fatal("concurrent update allowed")
	}
	unlock()
	m.Validate = nil
	if err := m.validate(context.Background(), m.Target, "v1.0.0"); err == nil {
		t.Fatal("non-ELF executable accepted")
	}
}
func TestCatalogRequiresOfficialAssetAndSupportsChecksums(t *testing.T) {
	c := Catalog{Repo: "ConsolePro/RemnaManager"}
	release := Release{Version: "v1.2.0", Assets: []Asset{{Name: "remanager-linux-amd64", URL: "https://evil.invalid/manager"}}}
	if _, err := c.Asset(release, "remanager-linux-amd64"); err == nil {
		t.Fatal("foreign release URL accepted")
	}
	digest := strings.Repeat("a", 64)
	release.Assets = []Asset{{Name: "checksums.txt", URL: "https://github.com/ConsolePro/RemnaManager/releases/download/v1.2.0/checksums.txt"}}
	c.Client = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(digest + "  remanager-linux-amd64\n"))}, nil
	})}
	got, err := c.Digest(context.Background(), release, Asset{Name: "remanager-linux-amd64"})
	if err != nil || got != digest {
		t.Fatal(got, err)
	}
}

func TestCatalogXrayChecksumFormats(t *testing.T) {
	for _, prefix := range []string{"SHA256= ", "SHA2-256(Xray-linux-64.zip)= ", "SHA256 (Xray-linux-64.zip) = "} {
		digest := strings.Repeat("a", 64)
		catalog := Catalog{Repo: "XTLS/Xray-core", Client: &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(prefix + digest + "\n"))}, nil
		})}}
		release := Release{Version: "v26.9.9", Assets: []Asset{{Name: "Xray-linux-64.zip.dgst", URL: "https://github.com/XTLS/Xray-core/releases/download/v26.9.9/Xray-linux-64.zip.dgst"}}}
		actual, err := catalog.Digest(context.Background(), release, Asset{Name: "Xray-linux-64.zip"})
		if err != nil || actual != digest {
			t.Fatal(prefix, actual, err)
		}
	}
}
