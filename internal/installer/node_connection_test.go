package installer

import (
	"context"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeConnectionDraftNeedsNoNameDomainOrAPIObjects(t *testing.T) {
	r, runner := managementFixture(t)
	seed := provisionJob(t)
	fields := strings.Fields(seed.Params.SSH.HostKey)
	fingerprint, err := hostKeyFingerprint(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	runner.response = func(call string, _ []byte) ([]byte, error) {
		if strings.HasPrefix(call, "ssh-keyscan ") {
			return []byte("node.example.test " + seed.Params.SSH.HostKey + "\n"), nil
		}
		t.Fatal("unexpected host operation", call)
		return nil, nil
	}
	// No test API server is configured: this action must not access /nodes,
	// profiles or keygen before root login and entering the actual details.
	result, err := r.nodeProvision(context.Background(), services.NodeRequest{Action: "node-install-connect-create", Fingerprint: fingerprint, Install: services.NodeInstallParameters{SSH: services.NodeSSH{Host: "node.example.test", Port: 22}}})
	if err != nil || result.Install == nil || !result.Install.DetailsPending || result.Install.Version != 4 {
		t.Fatal(result, err)
	}
	j, err := r.loadNodeJob(result.Install.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Params.Address != "node.example.test" || j.NodeUUID != "" || j.ProfileUUID != "" || j.Secret != "" || len(j.ShortIDs) < 3 || len(j.ShortIDs) > 6 {
		t.Fatal("draft created API resources or lost keys")
	}
	if _, err := r.nodeProvision(context.Background(), services.NodeRequest{Action: "node-install-details", JobID: j.ID}); err == nil {
		t.Fatal("details accepted before key setup")
	}
	if err := r.runNodeStage(context.Background(), &j, 0); err == nil {
		t.Fatal("installation started before details")
	}
}
