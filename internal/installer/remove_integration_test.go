package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestDockerPanelRemoveAndInstallAgain(t *testing.T) {
	if os.Getenv("RM_REMOVE_INTEGRATION") != "1" {
		t.Skip("opt-in disposable Docker resources")
	}
	r, _ := managementFixture(t)
	r.SystemRoot = t.TempDir()
	id, err := secret()
	if err != nil {
		t.Fatal(err)
	}
	project := "rmgo-remove-test-" + id[:16]
	runner := dockerProjectRunner{project: project, stack: r.Stack, real: r}
	r.Runner = runner
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	foreignVolume := project + "-unrelated"
	if _, err := runner.Run(ctx, nil, "docker", "volume", "create", foreignVolume); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = runner.Run(cleanup, nil, "docker", "compose", "--project-directory", r.Stack, "-f", filepath.Join(r.Stack, "fixture.json"), "down", "--volumes", "--remove-orphans")
		_, _ = runner.Run(cleanup, nil, "docker", "volume", "rm", foreignVolume)
	}()
	volumes := map[string]any{}
	var mounts []string
	for _, name := range []string{"database", "valkey-socket", "tinyauth-data", "mfa-data"} {
		volumes[name] = map[string]any{}
		mounts = append(mounts, name+":/fixture/"+name)
	}
	manifest, _ := json.Marshal(map[string]any{
		"services": map[string]any{"remnawave": map[string]any{"image": "busybox:1.37", "command": []string{"sleep", "600"}, "volumes": mounts, "networks": []string{"panel"}}},
		"volumes":  volumes, "networks": map[string]any{"panel": map[string]any{}},
	})
	fixture := filepath.Join(r.Stack, "fixture.json")
	if err := os.WriteFile(fixture, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		t.Logf("install/remove cycle %d", cycle+1)
		if cycle > 0 {
			r.state, err = newState(testParams())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.configure(); err != nil {
				t.Fatal(err)
			}
			if err := saveState(statePath(r.Root), r.state); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := runner.Run(ctx, nil, "docker", "compose", "-f", fixture, "up", "-d"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.manage(ctx, services.PanelRequest{Action: "panel-remove", Value: "DELETE"}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"volume", "network"} {
			output, err := runner.Run(ctx, nil, "docker", kind, "ls", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Name}}")
			if err != nil || strings.TrimSpace(string(output)) != "" {
				t.Fatalf("%s left after removal: %s (%v)", kind, output, err)
			}
		}
		if _, err := runner.Run(ctx, nil, "docker", "volume", "inspect", foreignVolume); err != nil {
			t.Fatal("unrelated volume lost", err)
		}
	}
}
