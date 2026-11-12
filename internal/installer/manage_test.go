package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

type managementRunner struct {
	calls    []string
	response func(string, []byte) ([]byte, error)
	dump     string
	dumpErr  error
	mfaUsers string
}

func (f *managementRunner) Run(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.response != nil {
		return f.response(call, input)
	}
	return []byte("running\n"), nil
}
func (f *managementRunner) RunTo(_ context.Context, out io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if strings.Contains(strings.Join(args, " "), "mfa-gateway cat /data/users.json") {
		_, err := io.WriteString(out, f.mfaUsers)
		return err
	}
	_, err := io.WriteString(out, f.dump)
	if err != nil {
		return err
	}
	return f.dumpErr
}

func TestMFABackupIncludesIdentityAndRejectsPartialData(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "corrupt"}[valid], func(t *testing.T) {
			r, f := managementFixture(t)
			r.state.Params.Protection = "mfa"
			if err := ensureProtection(&r.state); err != nil {
				t.Fatal(err)
			}
			if err := r.configure(); err != nil {
				t.Fatal(err)
			}
			f.mfaUsers = `{"users":[{"username":"admin","mfa":["synthetic-secret"]}]}`
			if !valid {
				f.mfaUsers = "incomplete-json"
			}
			directory, err := r.backup(context.Background())
			if !valid {
				if err == nil {
					t.Fatal("invalid MFA identity backup marked complete")
				}
				paths, _ := filepath.Glob(filepath.Join(r.Root, "backup-*", "COMPLETE"))
				if len(paths) != 0 {
					t.Fatal("corrupt backup has COMPLETE marker")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"mfa.env", "mfa.Caddyfile", "mfa-users.json", "COMPLETE"} {
				if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
					t.Fatal(name, err)
				}
			}
			data, _ := os.ReadFile(filepath.Join(directory, "mfa-users.json"))
			if string(data) != f.mfaUsers {
				t.Fatal("MFA factors were lost from backup")
			}
		})
	}
}
func managementFixture(t *testing.T) (*Real, *managementRunner) {
	t.Helper()
	r := NewReal()
	r.Root = t.TempDir()
	if runtime.GOOS == "linux" {
		// t.TempDir includes the test/subtest name. Long names can exceed the
		// Unix socket path limit, unrelated to the operation being tested.
		root, err := os.MkdirTemp(os.TempDir(), "rmgo-")
		if err != nil {
			t.Fatal(err)
		}
		r.Root = root
		t.Cleanup(func() {
			if filepath.Dir(root) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(root), "rmgo-") {
				t.Error("unexpected fixture cleanup path")
				return
			}
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
	}
	r.Stack = t.TempDir()
	r.state, _ = newState(testParams())
	r.state.BackendTag = "3.4.9"
	r.state.Ready = true
	r.state.ManagerToken = "manager-token-123456789"
	r.state.SubToken = "sub-token-123456789"
	if err := r.configure(); err != nil {
		t.Fatal(err)
	}
	if err := saveState(statePath(r.Root), r.state); err != nil {
		t.Fatal(err)
	}
	// Unit tests do not acquire a host lock; Linux flock is covered separately.
	var err error
	r.lock, err = os.CreateTemp(t.TempDir(), "test-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	f := &managementRunner{dump: "PGDMP-test-custom-format"}
	r.Runner = f
	return r, f
}
func TestManagementOwnershipAndRecoveryGuards(t *testing.T) {
	r, f := managementFixture(t)
	if err := os.Remove(filepath.Join(r.Stack, ".remnamanager-go")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-stop"}); err == nil {
		t.Fatal("modified unowned stack")
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
	if err := writeFile(filepath.Join(r.Stack, ".remnamanager-go"), []byte("managed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.state.RecoveryBackup = "private-backup"
	saveState(statePath(r.Root), r.state)
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-start"}); err == nil {
		t.Fatal("started backend needing recovery")
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-status"}); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsRecoveredWithoutStackOrRunner(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	r.Stack = filepath.Join(t.TempDir(), "missing-stack")
	r.Runner = nil // Credentials must not depend on any host command.
	s, err := newState(testParams())
	if err != nil {
		t.Fatal(err)
	}
	if err = saveState(statePath(r.Root), s); err != nil {
		t.Fatal(err)
	}
	for _, pending := range []bool{false, true} {
		s.FirewallPending = pending
		if err = saveState(statePath(r.Root), s); err != nil {
			t.Fatal(err)
		}
		result, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-credentials"})
		if err != nil || !strings.Contains(result.Text, s.AdminPassword) || !strings.Contains(result.TextRU, s.AdminPassword) {
			t.Fatal("could not recover saved password", err)
		}
		if r.lock != nil || r.log != nil {
			t.Fatal("reading credentials acquired an operation lock or created a log")
		}
		loaded, err := loadState(statePath(r.Root))
		if err != nil || loaded.AdminPassword != s.AdminPassword || loaded.FirewallPending != pending {
			t.Fatal("reading credentials changed the installation state", err)
		}
	}
}
func TestDatabaseBackupStreamsAndMarksCompletion(t *testing.T) {
	r, f := managementFixture(t)
	result, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-backup"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database.dump", "compose.json", ".env", "subscription.env", "panel.json", "COMPLETE"} {
		if _, err := os.Stat(filepath.Join(result.Text, name)); err != nil {
			t.Fatal(name, err)
		}
	}
	if strings.Contains(strings.Join(f.calls, "\n"), r.state.DBPassword) {
		t.Fatal("secret in process arguments")
	}
	f.dumpErr = errors.New("connection lost")
	if _, err := r.backup(context.Background()); err == nil {
		t.Fatal("incomplete dump accepted")
	}
	f.dumpErr = nil
	f.dump = "not a PostgreSQL dump"
	if _, err := r.backup(context.Background()); err == nil {
		t.Fatal("invalid format accepted")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBackendUpdateFailureKeepsRecoveryRecord(t *testing.T) {
	r, f := managementFixture(t)
	r.state.BackendTag = "3.4.8"
	saveState(statePath(r.Root), r.state)
	r.Client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"3.4.9","draft":false,"prerelease":false}`)), Header: make(http.Header)}, nil
	})}
	before, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	f.response = func(call string, _ []byte) ([]byte, error) {
		if strings.Contains(call, " up ") {
			return nil, errors.New("migration failed")
		}
		return nil, nil
	}
	_, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-update", Value: "3.4.9"})
	if err == nil {
		t.Fatal("failed update accepted")
	}
	after, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	if string(before) != string(after) {
		t.Fatal("previous configuration not restored")
	}
	s, err := loadState(statePath(r.Root))
	if err != nil || s.RecoveryBackup == "" || s.BackendTag != "3.4.8" {
		t.Fatal("lost recovery record", err)
	}
	if _, err = os.Stat(filepath.Join(s.RecoveryBackup, "COMPLETE")); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.calls, "\n")
	if !strings.Contains(calls, "stop remnawave") {
		t.Fatal("potentially migrated backend was not stopped")
	}
	if strings.Count(calls, " up ") != 1 {
		t.Fatal("unsafe automatic backend downgrade", calls)
	}
}
func TestSubscriptionUpdateRestoresImageOnFailure(t *testing.T) {
	r, f := managementFixture(t)
	before, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	ups := 0
	f.response = func(call string, _ []byte) ([]byte, error) {
		if strings.Contains(call, "image inspect") {
			return []byte("remnawave/subscription-page@sha256:" + strings.Repeat("a", 64)), nil
		}
		if strings.Contains(call, " up ") {
			ups++
			if ups == 1 {
				return nil, errors.New("new image failed")
			}
		}
		return nil, nil
	}
	if _, err := r.updateSubscription(context.Background(), "remnawave/subscription-page:latest"); err == nil {
		t.Fatal("failed image accepted")
	}
	after, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	if string(before) != string(after) || ups != 2 {
		t.Fatal("failed image was not reverted")
	}
}

func TestInterruptedSubscriptionUpdateRecoversOnStart(t *testing.T) {
	r, f := managementFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { io.WriteString(w, `{"response":{}}`) }))
	defer server.Close()
	r.APIBase = server.URL + "/api"
	r.Client = server.Client()
	before, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	r.state.SubRecovery = filepath.Join(r.Root, "subscription-before-update.json")
	writeFile(r.state.SubRecovery, before, 0600)
	saveState(statePath(r.Root), r.state)
	r.setComposeImage("subscription", "untrusted/broken:latest")
	f.response = func(call string, _ []byte) ([]byte, error) {
		if strings.Contains(call, " ps -q ") {
			return []byte("test-subscription-id"), nil
		}
		if strings.HasPrefix(call, "docker inspect ") {
			return []byte("healthy"), nil
		}
		return nil, nil
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "sub-update"}); err == nil {
		t.Fatal("ignored pending subscription recovery")
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "sub-start"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	if string(before) != string(after) {
		t.Fatal("did not restore previous image before start")
	}
	s, _ := loadState(statePath(r.Root))
	if s.SubRecovery != "" {
		t.Fatal("recovery was not committed")
	}
}

func TestMissingSubscriptionHealthcheckRejected(t *testing.T) {
	r, f := managementFixture(t)
	f.response = func(call string, _ []byte) ([]byte, error) {
		if strings.Contains(call, " ps -q ") {
			return []byte("test-id"), nil
		}
		return []byte("missing-healthcheck"), nil
	}
	if err := r.waitSubscription(context.Background()); err == nil || !strings.Contains(err.Error(), "HEALTHCHECK") {
		t.Fatal("unverified custom image accepted", err)
	}
}
func TestTokenReplacementAndLogRedaction(t *testing.T) {
	r, _ := managementFixture(t)
	old := r.state.ManagerToken
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer replacement-token-123456789" {
			w.WriteHeader(403)
			return
		}
		io.WriteString(w, `{"response":{"nodes":[]}}`)
	}))
	defer server.Close()
	r.APIBase = server.URL + "/api"
	r.Client = server.Client()
	if err := r.changeToken(context.Background(), "wrong-token-123456789"); err == nil || r.state.ManagerToken != old {
		t.Fatal("invalid token persisted")
	}
	if err := r.changeToken(context.Background(), "replacement-token-123456789"); err != nil {
		t.Fatal(err)
	}
	s, _ := loadState(statePath(r.Root))
	if s.ManagerToken != "replacement-token-123456789" {
		t.Fatal("new token not persisted")
	}
	text := r.safeOutput([]byte("\x1b[31m" + s.ManagerToken + "\x1b[0m\x1b]52;c;evil\x07\n" + s.DBPassword))
	if strings.Contains(text, s.ManagerToken) || strings.Contains(text, s.DBPassword) || strings.Contains(text, "\x1b") {
		t.Fatal("unsafe diagnostic output")
	}
}
func TestComposeImageEditPreservesOtherSettings(t *testing.T) {
	r, _ := managementFixture(t)
	path := filepath.Join(r.Stack, "compose.json")
	data, _ := os.ReadFile(path)
	var doc map[string]any
	json.Unmarshal(data, &doc)
	doc["x-test"] = "preserve"
	data, _ = json.Marshal(doc)
	writeFile(path, data, 0600)
	if _, err := r.setComposeImage("subscription", "remnawave/subscription-page:test"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &doc)
	if doc["x-test"] != "preserve" {
		t.Fatal("unrelated settings lost")
	}
	for _, value := range []string{"latest", "3.4", "3.4.9;id", "3.4.10-beta"} {
		if ValidateBackendVersion(value) == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"-evil", "repo/image;id", "repo/image\n", "repo/image@sha256:bad"} {
		if ValidateSubscriptionImage(value) == nil {
			t.Fatal(value)
		}
	}
}
