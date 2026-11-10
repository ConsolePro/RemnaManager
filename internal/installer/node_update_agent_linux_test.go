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
	// Reinstall a missing container from the same pinned image, including the
	// explicit force-recreate path that an ordinary same-image update skips.
	j.Image = "remnawave/node@sha256:" + strings.Repeat("b", 64)
	j.UpdateReinstall = true
	for index, fail := range []bool{false, true} {
		j.UpdateID = strings.Repeat(string(rune('4'+index)), 32)
		for _, name := range []string{"missing-node", "require-recreate"} {
			if err := os.WriteFile(fixture+name, []byte("1"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if phase, image, err := r.runNodeUpdateAgent(ctx, j, "prepare"); err != nil || phase != "prepared" || image != j.Image {
			t.Fatal("reinstall prepare", phase, err)
		}
		pulled, err := os.ReadFile(fixture + "last-pull")
		if err != nil || string(pulled) != j.Image {
			t.Fatal("reinstall pulled a different version", err)
		}
		if fail {
			if err := os.WriteFile(fixture+"update-fail", []byte("1"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		_, _, err = r.runNodeUpdateAgent(ctx, j, "apply")
		if (err != nil) != fail {
			t.Fatal("reinstall start", err)
		}
		action, want := "commit", "committed"
		if fail {
			action, want = "rollback", "rolled-back"
		}
		if phase, _, err := r.runNodeUpdateAgent(ctx, j, action); err != nil || phase != want {
			t.Fatal("reinstall finish", phase, err)
		}
		after, err = os.ReadFile("/opt/remnanode/compose.json")
		if err != nil || string(after) != string(committed) {
			t.Fatal("reinstall changed Compose", err)
		}
		if _, err = os.Stat(fixture + "missing-node"); !os.IsNotExist(err) {
			t.Fatal("container was not recreated")
		}
	}
}
