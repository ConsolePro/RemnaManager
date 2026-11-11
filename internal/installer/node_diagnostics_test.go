package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestChecksumFailureReachesUIInRussianWithoutSecretsOrDuplicateLogPaths(t *testing.T) {
	r, runner := managementFixture(t)
	j := provisionJob(t)
	j.Version = 2
	j.Image = "remnawave/node@sha256:" + strings.Repeat("a", 64)
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	for stage := 0; stage < 4; stage++ {
		j.Completed[stage] = true
	}
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	transportErr := errors.New("remote command failed")
	runner.response = func(string, []byte) ([]byte, error) {
		return []byte(j.Secret + "\n" + j.RealityKey + "\nRM_NODE_FAILURE=acme-checksum\nNode stage 3 failed; remote log: /var/lib/remnamanager-node/install.log\n"), transportErr
	}
	_, err := r.nodeProvision(context.Background(), services.NodeRequest{Action: "node-install-step", JobID: j.ID, Stage: 4})
	if err == nil || !errors.Is(err, transportErr) {
		t.Fatal("transport error lost", err)
	}
	message := services.ErrorText(err, "ru")
	if !strings.Contains(message, "Этап «Сайт selfsteal") || !strings.Contains(message, "ACME-клиент не прошёл проверку SHA256") {
		t.Fatal("specific failure not translated", message)
	}
	if strings.Contains(message, j.Secret) || strings.Contains(message, j.RealityKey) || strings.Count(message, "/var/lib/remnamanager-node/install.log") != 1 {
		t.Fatal("secrets or duplicate log paths in UI")
	}
	saved, err := r.loadNodeJob(j.ID)
	if err != nil || saved.Completed[4] || saved.Secret != j.Secret || saved.RealityKey != j.RealityKey {
		t.Fatal("failure damaged resume state", err)
	}
}
