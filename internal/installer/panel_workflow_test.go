package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestEncryptedStateLegacyMigrationAndTamper(t *testing.T) {
	s, err := newState(testParams())
	if err != nil {
		t.Fatal(err)
	}
	s.ManagerToken = "fixture-private-token"
	path := statePath(t.TempDir())
	legacy, _ := json.Marshal(s)
	if err = os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path)
	if err != nil || !reflect.DeepEqual(loaded, s) {
		t.Fatal("legacy state unreadable", err)
	}
	if err = saveState(path, loaded); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{s.DBPassword, s.AdminPassword, s.ManagerToken, s.CookieValue} {
		if strings.Contains(string(data), secret) {
			t.Fatal("plaintext secret in metadata")
		}
	}
	loaded, err = loadState(path)
	if err != nil || !reflect.DeepEqual(loaded, s) {
		t.Fatal("encrypted roundtrip failed", err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	sealed := doc["EncryptedSecrets"].(string)
	doc["EncryptedSecrets"] = "AAAA" + sealed[4:]
	data, _ = json.Marshal(doc)
	os.WriteFile(path, data, 0600)
	if _, err = loadState(path); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestBackupIntegrityRejectsTamperAndUnsafeSelection(t *testing.T) {
	r, _ := managementFixture(t)
	path, err := r.backup(context.Background(), "pre-update")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	m, err := r.verifyBackup(name)
	if err != nil || m.Reason != "pre-update" {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(path, "database.dump"), []byte("PGDMP-corrupt"), 0600)
	if _, err = r.verifyBackup(name); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	for _, name := range []string{"..", "../backup-20260101-000000-1", "C:/Windows", "backup-20260101-000000-1/.."} {
		if err = r.deleteBackup(name); err == nil {
			t.Fatal("unsafe deletion accepted", name)
		}
	}
}

func TestRemoteSubscriptionNeverUsesLocalDocker(t *testing.T) {
	r, f := managementFixture(t)
	key := provisionJob(t).Params.SSH.HostKey
	remote := services.SubscriptionRemote{SSH: services.NodeSSH{Host: "192.0.2.40", Port: 2222, HostKey: key}, User: "operator", Proxy: "caddy"}
	r.state.Params.RemoteSubscription = &remote
	saveState(statePath(r.Root), r.state)
	f.response = func(call string, _ []byte) ([]byte, error) {
		if !strings.HasPrefix(call, "ssh ") {
			return nil, errors.New("unexpected local command: " + call)
		}
		return []byte("remote healthy"), nil
	}
	for _, action := range []string{"sub-start", "sub-stop", "sub-restart", "sub-status", "sub-update", "sub-image"} {
		if _, err := r.manage(context.Background(), services.PanelRequest{Action: action, Value: "example/sub:stable"}); err != nil {
			t.Fatal(action, err)
		}
	}
	for _, call := range f.calls {
		if !strings.Contains(call, "StrictHostKeyChecking=yes") || !strings.Contains(call, "operator@192.0.2.40 sudo -n bash -s") {
			t.Fatal("unsafe remote command", call)
		}
	}
}

func TestRemoteStackHasNoPanelAndMainHasNoRemoteDependency(t *testing.T) {
	s, _ := newState(testParams())
	s.Params.RemoteSubscription = &services.SubscriptionRemote{User: "root", Proxy: "nginx"}
	main, err := RenderStack(s)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	json.Unmarshal(main, &doc)
	var svcs map[string]any
	json.Unmarshal(doc["services"], &svcs)
	if _, ok := svcs["subscription"]; ok {
		t.Fatal("remote subscription defined locally")
	}
	remote, env, config, err := renderRemoteSubscription(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(remote), "postgres") || strings.Contains(string(remote), "backend") || strings.Contains(string(env), "http://remnawave:") {
		t.Fatal("remote installed panel or used local API")
	}
	if !strings.Contains(config, "subscription:3010") {
		t.Fatal(config)
	}
	for _, proxy := range []string{"caddy", "nginx"} {
		s.Params.Proxy = proxy
		config = dockerProxyConfig(s, true)
		if strings.Contains(config, "subscription:3010") {
			t.Fatal("main proxy depends on remote container", proxy)
		}
	}
}

func TestNewLayoutKeepsComposeOperationsOnTheirOwnProject(t *testing.T) {
	r, _ := managementFixture(t)
	r.Stack = t.TempDir()
	r.state.LayoutVersion = 1
	if err := r.configure(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker-compose.yml", ".env", "subscription/docker-compose.yml", "subscription/.env"} {
		if _, err := os.Stat(filepath.Join(r.Stack, name)); err != nil {
			t.Fatal(name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.Stack, "compose.json")); !os.IsNotExist(err) {
		t.Fatal("duplicate legacy manifest created")
	}
	main := strings.Join(r.composeArgs("--profile", "subscription", "ps", "--all"), " ")
	sub := strings.Join(r.composeArgs("--profile", "subscription", "restart", "subscription"), " ")
	if strings.Contains(main, filepath.Join("subscription", "docker-compose.yml")) || !strings.Contains(sub, filepath.Join("subscription", "docker-compose.yml")) {
		t.Fatal("Compose action routed to wrong project", main, sub)
	}
}

func TestLatestImageReferenceIsNotInstalledVersion(t *testing.T) {
	r, f := managementFixture(t)
	f.response = func(call string, _ []byte) ([]byte, error) {
		switch {
		case strings.Contains(call, " ps "):
			return []byte("backend-id"), nil
		case strings.Contains(call, "image inspect"):
			return []byte("<no value>"), nil
		case strings.Contains(call, ".Config.Image"):
			return []byte("remnawave/backend:latest"), nil
		default:
			return []byte("sha256:fixture"), nil
		}
	}
	if _, err := r.installedBackendVersion(context.Background()); err == nil {
		t.Fatal("latest accepted as an installed version")
	}
}
