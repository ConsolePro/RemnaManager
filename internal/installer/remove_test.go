package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestRemovePanelAllProxiesAndProtections(t *testing.T) {
	for _, proxy := range []string{"nginx", "caddy"} {
		for _, protection := range []string{"cookie", "tinyauth", "mfa"} {
			t.Run(proxy+"-"+protection, func(t *testing.T) {
				r, runner := managementFixture(t)
				r.SystemRoot = t.TempDir()
				r.state.Params.Proxy, r.state.Params.Protection = proxy, protection
				if protection == "tinyauth" {
					r.state.Params.AuthDomain = "auth." + r.state.Params.PanelDomain
				}
				r.state.Completed[1], r.state.Completed[2] = true, true
				if err := ensureProtection(&r.state); err != nil {
					t.Fatal(err)
				}
				if err := r.configure(); err != nil {
					t.Fatal(err)
				}
				config := RenderCaddy(r.state)
				if proxy == "nginx" {
					config = RenderNginx(r.state, true)
				}
				proxyPath := r.removalProxyPath()
				if err := writeFile(proxyPath, []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				if err := saveState(statePath(r.Root), r.state); err != nil {
					t.Fatal(err)
				}
				backup := filepath.Join(r.Root, "backup-example", "COMPLETE")
				certificate := r.systemPath("/etc/letsencrypt/live/example/cert.pem")
				for _, path := range []string{backup, certificate, filepath.Join(r.Stack, "user-note.txt")} {
					if err := writeFile(path, []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				var gotManifest bool
				runner.response = func(call string, _ []byte) ([]byte, error) {
					if strings.Contains(call, " down --volumes --remove-orphans") {
						matches, _ := filepath.Glob(filepath.Join(r.Root, "remove-*.json"))
						if len(matches) != 1 {
							t.Fatal(matches)
						}
						data, _ := os.ReadFile(matches[0])
						var manifest struct {
							Name     string
							Volumes  map[string]any
							Services map[string]any
						}
						if json.Unmarshal(data, &manifest) != nil || manifest.Name != "remnamanager-panel" || len(manifest.Volumes) != 4 || len(manifest.Services) != 6 {
							t.Fatal(string(data))
						}
						gotManifest = true
					}
					return []byte("running\n"), nil
				}
				result, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-remove", Value: "DELETE"})
				if err != nil {
					t.Fatal(err)
				}
				if !gotManifest || result.TextRU == "" || r.Installed() {
					t.Fatal(result)
				}
				for _, path := range []string{proxyPath, filepath.Join(r.Stack, "compose.json"), filepath.Join(r.Stack, ".env"), filepath.Join(r.Stack, ".remnamanager-go")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("not removed", path, err)
					}
				}
				for _, path := range []string{backup, certificate, filepath.Join(r.Stack, "user-note.txt")} {
					if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
						t.Fatal("preserved content lost", path, err)
					}
				}
				state, err := loadState(statePath(r.Root))
				if err != nil || !state.Removed || state.AdminPassword != "" || state.Params.PanelDomain != "" {
					t.Fatal(state, err)
				}
			})
		}
	}
}

func TestRemoveRequiresConsentAndRejectsForeignProxy(t *testing.T) {
	r, runner := managementFixture(t)
	r.SystemRoot = t.TempDir()
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-remove"}); err == nil || len(runner.calls) != 0 {
		t.Fatal("removal without consent", err)
	}
	if err := writeFile(r.removalProxyPath(), []byte("foreign configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-remove", Value: "DELETE"}); err == nil || len(runner.calls) != 0 {
		t.Fatal("foreign proxy was modified", err)
	}
	if data, _ := os.ReadFile(r.removalProxyPath()); string(data) != "foreign configuration" {
		t.Fatal("foreign proxy lost")
	}
}

func TestInterruptedRemovalResumesAndBlocksOtherManagement(t *testing.T) {
	r, runner := managementFixture(t)
	r.SystemRoot = t.TempDir()
	fail := true
	runner.response = func(call string, _ []byte) ([]byte, error) {
		if fail && strings.Contains(call, " down --volumes") {
			return nil, errors.New("simulated Docker failure")
		}
		return nil, nil
	}
	request := services.PanelRequest{Action: "panel-remove", Value: "DELETE"}
	if _, err := r.manage(context.Background(), request); err == nil {
		t.Fatal("failure ignored")
	}
	state, err := loadState(statePath(r.Root))
	if err != nil || !state.RemovalPending || state.RemovalPhase != 1 {
		t.Fatal(state, err)
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-start"}); err == nil {
		t.Fatal("start allowed during removal")
	}
	fail = false
	if _, err := r.manage(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if state, err = loadState(statePath(r.Root)); err != nil || !state.Removed {
		t.Fatal(state, err)
	}
}

func TestRemoveIncompleteInstallationWithoutStackMarker(t *testing.T) {
	r, _ := managementFixture(t)
	r.SystemRoot = t.TempDir()
	r.state.Ready = false
	r.state.ProtectionPending = true
	if err := os.Remove(filepath.Join(r.Stack, ".remnamanager-go")); err != nil {
		t.Fatal(err)
	}
	if err := saveState(statePath(r.Root), r.state); err != nil {
		t.Fatal(err)
	}
	if _, err := r.manage(context.Background(), services.PanelRequest{Action: "panel-remove", Value: "DELETE"}); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalDoesNotFollowSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if err := removeKnownFile(link); err == nil {
		t.Fatal("symlink deletion accepted")
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Fatal("target changed")
	}
}

func TestRemovalDeletesOnlyOwnedFirewallTableAndStopsRollbackFirst(t *testing.T) {
	r, runner := managementFixture(t)
	r.SystemRoot = t.TempDir()
	r.state.FirewallOwned, r.state.FirewallPending = true, true
	unit := fmt.Sprintf("[Unit]\nDescription=RemnaManager firewall\nAfter=network-pre.target\nBefore=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s/firewall-apply.sh\nExecReload=%s/firewall-apply.sh\n[Install]\nWantedBy=multi-user.target\n", r.Root, r.Root)
	path := r.systemPath("/etc/systemd/system/remnamanager-firewall.service")
	if err := writeFile(path, []byte(unit), 0600); err != nil {
		t.Fatal(err)
	}
	runner.response = func(call string, _ []byte) ([]byte, error) {
		if call == "nft list tables" {
			return []byte("table inet foreign\ntable inet remnamanager_go\ntable ip docker-bridges\n"), nil
		}
		return nil, nil
	}
	if err := r.removePanelFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.calls, "\n")
	stop := strings.Index(calls, "systemctl stop "+rollbackUnit+".timer")
	delete := strings.Index(calls, "nft delete table inet remnamanager_go")
	if stop < 0 || delete < stop || strings.Contains(calls, "flush") || strings.Contains(calls, "delete table inet foreign") || strings.Contains(calls, "delete table ip") {
		t.Fatal(calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("firewall unit left", err)
	}
}

func TestRemovalResumesAfterFileCleanupFailureWithoutDeletingVolumesAgain(t *testing.T) {
	r, runner := managementFixture(t)
	r.SystemRoot = t.TempDir()
	if err := os.Remove(filepath.Join(r.Stack, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(r.Stack, ".env"), 0700); err != nil {
		t.Fatal(err)
	}
	request := services.PanelRequest{Action: "panel-remove", Value: "DELETE"}
	if _, err := r.manage(context.Background(), request); err == nil {
		t.Fatal("non-regular file was removed")
	}
	if r.state.RemovalPhase != 2 || !r.state.RemovalPending {
		t.Fatal("cleanup progress not saved")
	}
	if err := os.Remove(filepath.Join(r.Stack, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.manage(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, call := range runner.calls {
		if strings.Contains(call, " down --volumes") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("volumes removed repeatedly", count)
	}
}
