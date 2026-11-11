package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestProgressProcessHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "rm-progress-helper" {
		return
	}
	fmt.Println("@RM_PROGRESS docker-install")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Args[len(os.Args)-1]); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(2)
}

func TestExecRunnerStreamsProgressWhileProcessIsRunning(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ctx = services.WithProgress(ctx, func(_, _ string) {
		_ = os.WriteFile(release, []byte("ready"), 0600)
	})
	output, err := (ExecRunner{}).Run(ctx, nil, os.Args[0], "-test.run=^TestProgressProcessHelper$", "--", "rm-progress-helper", release)
	if err != nil {
		t.Fatalf("progress was not delivered while command was running: %v; %s", err, output)
	}
	if !strings.Contains(string(output), "@RM_PROGRESS docker-install") {
		t.Fatal("command output was lost")
	}
}

func TestSetupProgressOnlyReportsKnownCompleteMarkers(t *testing.T) {
	var got []string
	w := &setupProgressWriter{ctx: services.WithProgress(context.Background(), func(_, en string) { got = append(got, en) })}
	for _, chunk := range []string{
		"secret-token\n@RM_PRO", "GRESS packages-update\n",
		"@RM_PROGRESS secret-password\n", "@RM_PROGRESS docker-install\n",
		strings.Repeat("x", 10000), "@RM_PROGRESS docker-ready\n",
		"@RM_PROGRESS docker-check\n",
	} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatal(n, err)
		}
	}
	want := []string{"Updating package lists", "Installing Docker Engine and Compose", "Checking Docker Engine and Compose versions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
