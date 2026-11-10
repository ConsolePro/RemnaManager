//go:build linux

package installer

import (
	"context"
	"os"
	"strings"
	"testing"
)

func checkNodeUpdateAgent(t *testing.T, r *Real, j NodeJob) {
	t.Helper()
	ctx := context.Background()
	fixture := "/var/lib/remnamanager-node-fixture/"
	if err := os.WriteFile(fixture+"update-mode", []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("/opt/remnanode/compose.json")
	if err != nil {
		t.Fatal(err)
	}
	j.UpdateID = strings.Repeat("1", 32)
	if phase, _, err := r.runNodeUpdateAgent(ctx, j, "prepare"); err != nil || phase != "prepared" {
		t.Fatal("prepare", phase, err)
	}
	if phase, _, err := r.runNodeUpdateAgent(ctx, j, "apply"); err != nil || phase != "trial" {
		t.Fatal("apply", phase, err)
	}
	// Execute the same independent script used by systemd and the reboot service.
	if _, err := (ExecRunner{}).Run(ctx, nil, "bash", "/var/lib/remnamanager-node/updates/"+j.UpdateID+"/rollback.sh"); err != nil {
		t.Fatal("independent rollback", err)
	}
	after, _ := os.ReadFile("/opt/remnanode/compose.json")
	if string(after) != string(original) {
		t.Fatal("rollback did not restore exact Compose")
	}
	if phase, _, err := r.runNodeUpdateAgent(ctx, j, "status"); err != nil || phase != "rolled-back" {
		t.Fatal(phase, err)
	}
	j.UpdateID = strings.Repeat("2", 32)
	if _, _, err := r.runNodeUpdateAgent(ctx, j, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture+"update-fail", []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.runNodeUpdateAgent(ctx, j, "apply"); err == nil {
		t.Fatal("accepted failed container start")
	}
	if phase, _, err := r.runNodeUpdateAgent(ctx, j, "rollback"); err != nil || phase != "rolled-back" {
		t.Fatal(phase, err)
	}
	os.Remove(fixture + "update-fail")
	j.UpdateID = strings.Repeat("3", 32)
	if _, _, err := r.runNodeUpdateAgent(ctx, j, "prepare"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.runNodeUpdateAgent(ctx, j, "apply"); err != nil {
		t.Fatal(err)
	}
	if phase, _, err := r.runNodeUpdateAgent(ctx, j, "commit"); err != nil || phase != "committed" {
		t.Fatal(phase, err)
	}
	committed, _ := os.ReadFile("/opt/remnanode/compose.json")
	if !strings.Contains(string(committed), strings.Repeat("b", 64)) || !strings.Contains(string(committed), j.Secret) {
		t.Fatal("new image or saved credentials missing")
	}
	if _, _, err := r.runNodeUpdateAgent(ctx, j, "rollback"); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile("/opt/remnanode/compose.json")
	if string(committed) != string(after) {
		t.Fatal("late rollback undid committed update")
	}
}
