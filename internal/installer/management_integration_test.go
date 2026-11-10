package installer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

type dockerProjectRunner struct {
	project, stack string
	real           *Real
	exec           ExecRunner
}

func (d dockerProjectRunner) args(args []string) []string {
	if len(args) > 0 && args[0] == "compose" {
		return append([]string{"compose", "--project-name", d.project}, args[1:]...)
	}
	return args
}
func (d dockerProjectRunner) Run(ctx context.Context, in []byte, name string, args ...string) ([]byte, error) {
	if name != "docker" {
		return nil, fmt.Errorf("integration runner forbids host command %s", name)
	}
	data, err := d.exec.Run(ctx, in, name, d.args(args)...)
	if err == nil && len(args) > 0 && args[0] == "compose" && strings.Contains(strings.Join(args, " "), "up ") {
		port, e := d.exec.Run(ctx, nil, "docker", "compose", "--project-name", d.project, "--project-directory", d.stack, "-f", filepath.Join(d.stack, "compose.json"), "port", "remnawave", "3000")
		if e == nil {
			d.real.APIBase = "http://" + strings.TrimSpace(string(port)) + "/api"
		}
	}
	return data, err
}
func (d dockerProjectRunner) RunTo(ctx context.Context, out io.Writer, name string, args ...string) error {
	if name != "docker" {
		return fmt.Errorf("integration runner forbids %s", name)
	}
	return d.exec.RunTo(ctx, out, name, d.args(args)...)
}

func TestLiveComponentManagement(t *testing.T) {
	root, base, project := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_PANEL_API"), os.Getenv("RM_MANAGEMENT_PROJECT")
	if root == "" || base == "" || !strings.HasPrefix(project, "rmgo-management-test-") {
		t.Skip("opt-in disposable management stack only")
	}
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.APIBase = base
	var err error
	r.lock, err = os.CreateTemp(r.Root, "integration-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, err := loadState(statePath(r.Root))
	if err != nil {
		t.Fatal(err)
	}
	// This fixture has no systemd/firewall host; their separate tests cover that lifecycle.
	s.Ready = true
	if err = saveState(statePath(r.Root), s); err != nil {
		t.Fatal(err)
	}
	r.Runner = dockerProjectRunner{project: project, stack: root, real: r}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	run := func(action, value string) services.PanelResult {
		t.Helper()
		t.Log("operation:", action)
		result, err := r.manage(ctx, services.PanelRequest{Action: action, Value: value})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		return result
	}
	status := run("panel-status", "")
	if !strings.Contains(status.Text, "remnawave") {
		t.Fatal("missing real component status")
	}
	backup := run("panel-backup", "")
	data, err := os.ReadFile(filepath.Join(backup.Text, "database.dump"))
	if err != nil || len(data) < 1000 || string(data[:5]) != "PGDMP" {
		t.Fatal("missing real PostgreSQL dump", err)
	}
	if _, err = r.Runner.Run(ctx, data, "docker", r.composeArgs("exec", "-T", "remnawave-db", "pg_restore", "--list")...); err != nil {
		t.Fatal("pg_restore rejected the dump", err)
	}
	logs := run("panel-logs", "")
	for _, secret := range []string{s.AdminPassword, s.ManagerToken, s.DBPassword} {
		if strings.Contains(logs.Text, secret) {
			t.Fatal("log exposed a secret")
		}
	}
	run("sub-stop", "")
	run("sub-start", "")
	run("sub-update", "")
	// Validate update orchestration against the same known backend image. This is
	// not an assertion that an actual cross-version database migration is safe.
	s, err = loadState(statePath(r.Root))
	if err != nil {
		t.Fatal(err)
	}
	s.BackendTag = "3.4.8"
	saveState(statePath(r.Root), s)
	update := run("panel-update", "3.4.9")
	if !strings.Contains(update.Text, "3.4.9") {
		t.Fatal("backend update flow did not finish")
	}
	// Revalidate and replace with the fixture's existing token without printing it.
	run("panel-token", s.ManagerToken)
	credentials := run("panel-credentials", "")
	if !strings.Contains(credentials.Text, s.AdminPassword) {
		t.Fatal("saved credentials unavailable")
	}
	run("panel-stop", "")
	run("panel-start", "")
	run("sub-start", "")
	status = run("panel-status", "")
	if !strings.Contains(status.Text, "remnawave") {
		t.Fatal("services did not restart")
	}
}
