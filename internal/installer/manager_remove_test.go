package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/updater"
)

func managerRemovalFixture(t *testing.T) (*Real, func(string) bool, map[string]string) {
	t.Helper()
	base := t.TempDir()
	r := &Real{Root: filepath.Join(base, "var/lib/remnamanager"), Stack: filepath.Join(base, "opt/remnawave"), SystemRoot: base}
	files := map[string]string{
		updater.ManagerTarget: "manager", updater.ManagerTarget + ".previous": "previous manager",
		updater.ManagerTarget + ".previous.json": "metadata", "/usr/local/lib/remnamanager/install.lock": "",
		updater.ManagerTarget + ".update.lock": "", "/etc/remnamanager/config.json": "config",
		"/etc/remnamanager/update-cache.json": "cache", "/etc/remnamanager/custom.txt": "keep custom config",
		"/usr/local/bin/remna": "panel CLI", "/opt/remnawave/compose.json": "panel stack",
		"/opt/remnanode/compose.json": "node stack", "/var/lib/remnamanager/panel.json": "panel state",
		"/var/lib/remnamanager/firewall-apply.sh": "firewall script", "/var/lib/remnamanager/firewall-active.nft": "firewall rules",
		"/var/lib/remnamanager/ssh/keys/node-test": "private key", "/var/lib/remnamanager/nodes/node.json": "node journal",
		"/var/lib/remnamanager/backups/db.sql": "database backup", "/var/lib/remnamanager/management-123.log": "log",
		"/etc/systemd/system/remnamanager-firewall.service": "panel firewall service",
	}
	for path, content := range files {
		path = r.systemPath(path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	inspect := func(path string) bool {
		data, err := os.ReadFile(path)
		return err == nil && (string(data) == "manager" || string(data) == "previous manager")
	}
	return r, inspect, files
}

func TestManagerRemovalPreservesPanelNodeDataAndForeignCommands(t *testing.T) {
	r, inspect, files := managerRemovalFixture(t)
	plan, err := r.managerRemovalPlan(context.Background(), inspect)
	if err != nil {
		t.Fatal(err)
	}
	// A malicious or stale UI cannot expand the filesystem scope of removal.
	plan.Remove = append(plan.Remove, r.systemPath("/opt/remnawave/compose.json"))
	if err := r.removeManagerFiles(context.Background(), plan, inspect); err != nil {
		t.Fatal(err)
	}
	for path, content := range files {
		remove := strings.HasPrefix(path, "/usr/local/lib/remnamanager/") || path == "/etc/remnamanager/config.json" || path == "/etc/remnamanager/update-cache.json" || path == "/var/lib/remnamanager/management-123.log"
		data, err := os.ReadFile(r.systemPath(path))
		if remove {
			if !os.IsNotExist(err) {
				t.Fatal("manager file remains", path, err)
			}
		} else if err != nil || string(data) != content {
			t.Fatal("component or foreign file changed", path, err)
		}
	}
}

func TestManagerRemovalRejectsChangesAndCancelledContext(t *testing.T) {
	for _, scenario := range []string{"changed", "cancelled", "pending-update", "foreign-binary"} {
		t.Run(scenario, func(t *testing.T) {
			r, inspect, _ := managerRemovalFixture(t)
			plan, err := r.managerRemovalPlan(context.Background(), inspect)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "changed":
				err = os.WriteFile(r.systemPath("/etc/remnamanager/config.json"), []byte("changed"), 0600)
			case "cancelled":
				cancel()
			case "pending-update":
				err = os.WriteFile(r.systemPath(updater.ManagerTarget+".update.json"), []byte("pending"), 0600)
			case "foreign-binary":
				err = os.WriteFile(r.systemPath(updater.ManagerTarget), []byte("other program"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := r.removeManagerFiles(ctx, plan, inspect); err == nil {
				t.Fatal("unsafe removal accepted")
			}
			if _, err := os.Stat(r.systemPath(updater.ManagerTarget)); err != nil {
				t.Fatal("executable removed after refusal", err)
			}
			if _, err := os.Stat(r.systemPath("/etc/remnamanager/config.json")); err != nil {
				t.Fatal("configuration removed after refusal", err)
			}
		})
	}
}

func TestFinalLogWritesAndNewLockDoNotInvalidateRemoval(t *testing.T) {
	r, inspect, _ := managerRemovalFixture(t)
	path := r.systemPath(updater.ManagerTarget + ".update.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	plan, err := r.managerRemovalPlan(context.Background(), inspect)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.systemPath("/var/lib/remnamanager/management-123.log"), []byte("SSH shutdown complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.removeManagerFiles(context.Background(), plan, inspect); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRemovalSymlinksAreNotFollowed(t *testing.T) {
	r, inspect, _ := managerRemovalFixture(t)
	alias := r.systemPath("/usr/local/bin/remanager")
	if err := os.Symlink("../lib/remnamanager/remanager", alias); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("symlink privilege unavailable")
		}
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("remanager", r.systemPath("/usr/local/bin/re")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("remna", r.systemPath("/usr/local/bin/remna_manager")); err != nil {
		t.Fatal(err)
	}
	plan, err := r.managerRemovalPlan(context.Background(), inspect)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.removeManagerFiles(context.Background(), plan, inspect); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"re", "remanager"} {
		if _, err := os.Lstat(r.systemPath("/usr/local/bin/" + name)); !os.IsNotExist(err) {
			t.Fatal("owned alias remains", name)
		}
	}
	if link, err := os.Readlink(r.systemPath("/usr/local/bin/remna_manager")); err != nil || link != "remna" {
		t.Fatal("foreign alias changed", err)
	}
}
