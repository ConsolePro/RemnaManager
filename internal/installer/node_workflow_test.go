package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeV4NamesComeFromNodeAndIncludeCountry(t *testing.T) {
	j := provisionJob(t)
	j.Version = 4
	j.Params.Name, j.Params.ProfileName = "FN1501", "Ignored"
	j.Params.CountryCode = "FI"
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	if nodeProfileName(j) != "VLESS_REALITY_FN1501" || nodeInboundTag(j) != nodeProfileName(j) {
		t.Fatal("profile and inbound must use the node name")
	}
	if nodeHostName(j) != "🇫🇮 Финляндия · VLESS_REALITY_FN1501" {
		t.Fatal(nodeHostName(j))
	}
	j.Params.HostName = "Fast"
	if nodeHostName(j) != "🇫🇮 Финляндия · Fast" {
		t.Fatal(nodeHostName(j))
	}
}

func TestSavedSSHKeyAvoidsScanAndPasswordAndFailureRequestsPassword(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved-key", true: "recover-key"}[failed], func(t *testing.T) {
			r, runner := managementFixture(t)
			j := provisionJob(t)
			j.Version = 4
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
			}
			keyPath, _, err := r.nodeManagedKey(j)
			if err != nil {
				t.Fatal(err)
			}
			j.Params.SSH.IdentityFile = keyPath
			if err := r.saveNodeJob(&j); err != nil {
				t.Fatal(err)
			}
			scanned := false
			newHost := provisionJob(t).Params.SSH.HostKey
			runner.response = func(call string, _ []byte) ([]byte, error) {
				if strings.HasPrefix(call, "ssh-keyscan ") {
					scanned = true
					return []byte(j.Params.SSH.Host + " " + newHost + "\n"), nil
				}
				if strings.HasSuffix(call, " true") {
					if !strings.Contains(call, "ControlPath=none") || !strings.Contains(call, "BatchMode=yes") {
						t.Fatal("connection reused an existing session", call)
					}
					if failed {
						return nil, errors.New("public key rejected")
					}
				}
				return nil, nil
			}
			result, err := r.nodeProvision(context.Background(), services.NodeRequest{Action: "node-install-auth", JobID: j.ID})
			if err != nil {
				t.Fatal(err)
			}
			if !failed {
				if scanned || len(result.Command) != 0 {
					t.Fatal("saved key should connect without scans or password")
				}
				return
			}
			command := strings.Join(result.Command, " ")
			if !scanned || !strings.Contains(command, "PubkeyAuthentication=no") || !strings.Contains(command, "StrictHostKeyChecking=yes") {
				t.Fatal("recovery must prompt for a password with a pinned server key", command)
			}
			saved, err := r.loadNodeJob(j.ID)
			if err != nil || saved.Params.SSH.IdentityFile != "" || saved.Params.SSH.HostKey != newHost {
				t.Fatal("recovery state not saved", err)
			}
			backups, err := filepath.Glob(keyPath + ".previous-*")
			if err != nil || len(backups) != 1 {
				t.Fatal("old key was not retained", err)
			}
			if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
				t.Fatal("new authentication must create a new key")
			}
		})
	}
}

func TestFirewallCannotCommitUsingAnExistingSSHSession(t *testing.T) {
	r, runner := managementFixture(t)
	j := provisionJob(t)
	j.Completed[7], j.FirewallPending = true, true
	j.RollbackAt = time.Now().Add(time.Minute)
	calls := 0
	runner.response = func(call string, input []byte) ([]byte, error) {
		calls++
		if len(input) > 0 || !strings.Contains(call, "ControlPath=none") {
			t.Fatal("firewall commit ran before fresh SSH succeeded")
		}
		return nil, errors.New("new inbound connection blocked")
	}
	if err := r.confirmNodeInstall(context.Background(), &j); err == nil {
		t.Fatal("firewall committed without inbound SSH")
	}
	if calls != 1 || !j.FirewallPending || j.Ready {
		t.Fatal("rollback must remain armed")
	}
}

func TestWebsiteApplyCancellationRollsBackUsingFreshContext(t *testing.T) {
	r, _, j := updateFixture(t)
	j.Version = 4
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rolledBack := false
	r.Runner = updateContextRunner{run: func(callCtx context.Context, input []byte) ([]byte, error) {
		script := string(input)
		switch {
		case strings.Contains(script, "RM_SITE_ACTION='status'"):
			return []byte("RM_SITE_PHASE=missing\n"), nil
		case strings.Contains(script, "RM_SITE_ACTION='apply'"):
			cancel()
			return nil, context.Canceled
		case strings.Contains(script, "RM_SITE_ACTION='rollback'"):
			if callCtx.Err() != nil {
				t.Fatal("cancelled context reused for rollback")
			}
			rolledBack = true
			return []byte("RM_SITE_PHASE=rolled-back\n"), nil
		}
		t.Fatal("unexpected remote command")
		return nil, nil
	}}
	_, err := r.manageNodeSite(ctx, services.NodeRequest{Action: "node-site-run", Value: "atlas"}, services.Node{UUID: j.NodeUUID, Address: j.Params.Address})
	if !errors.Is(err, context.Canceled) || !rolledBack {
		t.Fatal("cancelled website change did not roll back", err)
	}
}
