package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestManagedNodeKeyIsUniquePersistentAndPrivate(t *testing.T) {
	r, _ := managementFixture(t)
	j := provisionJob(t)
	path, public, err := r.nodeManagedKey(j)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatal("invalid managed private key", err)
	}
	again, againPublic, err := r.nodeManagedKey(j)
	if err != nil || again != path || public != againPublic {
		t.Fatal("key changed on retry", err)
	}
	j.ID = strings.Repeat("b", 32)
	other, otherPublic, err := r.nodeManagedKey(j)
	if err != nil || other == path || public == otherPublic {
		t.Fatal("nodes share a key", err)
	}
	if strings.Contains(public, "PRIVATE KEY") {
		t.Fatal("private material in authorized key")
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	j.ID = strings.Repeat("a", 32)
	if _, _, err := r.nodeManagedKey(j); err == nil {
		t.Fatal("corrupt key silently regenerated")
	}
}

func TestManagedKeyIsSavedOnlyAfterFreshKeyOnlyLogin(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "failed-verification"}[failVerification], func(t *testing.T) {
			r, runner := managementFixture(t)
			j := provisionJob(t)
			if err := r.saveNodeJob(&j); err != nil {
				t.Fatal(err)
			}
			var sawPublic, sawVerify bool
			runner.response = func(call string, input []byte) ([]byte, error) {
				if strings.HasSuffix(call, "sh -s") {
					sawPublic = true
					if !strings.Contains(string(input), "grep -Fqx") || !strings.Contains(string(input), "authorized_keys") || strings.Contains(string(input), "PRIVATE KEY") {
						t.Fatal("unsafe public key installation", string(input))
					}
				}
				if strings.HasSuffix(call, " true") {
					sawVerify = true
					for _, option := range []string{"ControlPath=none", "IdentityAgent=none", "BatchMode=yes", "PreferredAuthentications=publickey", "StrictHostKeyChecking=yes"} {
						if !strings.Contains(call, option) {
							t.Fatal("verification can reuse a session or skip pin", call)
						}
					}
					if failVerification {
						return nil, errors.New("key rejected")
					}
				}
				return nil, nil
			}
			err := r.installNodeManagedKey(context.Background(), &j)
			if !sawPublic || !sawVerify || (err != nil) != failVerification {
				t.Fatal(err, runner.calls)
			}
			loaded, err := r.loadNodeJob(j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failVerification {
				if loaded.Params.SSH.IdentityFile != "" {
					t.Fatal("unverified key became the login identity")
				}
			} else if loaded.Params.SSH.IdentityFile != filepath.Join(r.Root, "ssh", "keys", "node-"+j.ID) {
				t.Fatal("verified identity not saved")
			}
		})
	}
}

func TestManagedKeyRequiresAuthenticatedSession(t *testing.T) {
	r, runner := managementFixture(t)
	j := provisionJob(t)
	runner.response = func(call string, _ []byte) ([]byte, error) { return nil, errors.New("no master") }
	if err := r.installNodeManagedKey(context.Background(), &j); err == nil {
		t.Fatal("missing session accepted")
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "-O check") {
		t.Fatal("attempted unauthenticated key installation", runner.calls)
	}
}

func TestExistingSSHIdentityIsKept(t *testing.T) {
	r, runner := managementFixture(t)
	j := provisionJob(t)
	j.Params.SSH.IdentityFile = "/existing/custom-key"
	if err := r.installNodeManagedKey(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 || j.Params.SSH.IdentityFile != "/existing/custom-key" {
		t.Fatal("existing identity replaced", runner.calls)
	}
}
