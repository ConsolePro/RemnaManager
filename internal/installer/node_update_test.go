package installer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

type updateContextRunner struct {
	run func(context.Context, []byte) ([]byte, error)
}

func (r updateContextRunner) Run(ctx context.Context, in []byte, _ string, _ ...string) ([]byte, error) {
	return r.run(ctx, in)
}

func TestNodeUpdateCancellationKeepsRecoveryAlive(t *testing.T) {
	r, _, j := updateFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phase := "prepared"
	target := "remnawave/node@sha256:" + strings.Repeat("b", 64)
	r.Runner = updateContextRunner{run: func(callCtx context.Context, in []byte) ([]byte, error) {
		if callCtx.Err() != nil {
			t.Fatal("cancelled context was reused for recovery")
		}
		payload := string(in)
		if strings.Contains(payload, "RM_UPDATE_ACTION='apply'") {
			phase = "trial"
			cancel()
		}
		if strings.Contains(payload, "RM_UPDATE_ACTION='rollback'") {
			phase = "rolled-back"
		}
		return []byte("RM_UPDATE_PHASE=" + phase + "\nRM_UPDATE_IMAGE=" + target + "\n"), nil
	}}
	if err := r.updateNode(ctx, &j); !errors.Is(err, context.Canceled) || phase != "rolled-back" || j.UpdateID != "" {
		t.Fatal("cancelled update was not recovered", err, phase)
	}
}

func updateFixture(t *testing.T) (*Real, *managementRunner, NodeJob) {
	t.Helper()
	r, runner := managementFixture(t)
	j := provisionJob(t)
	j.NodeUUID = testNodeUUID
	j.Ready = true
	for i := 0; i < 9; i++ {
		j.Completed[i] = true
	}
	j.Image = "remnawave/node@sha256:" + strings.Repeat("a", 64)
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("update changed panel resources", req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": services.Node{UUID: j.NodeUUID, Address: j.Params.Address, IsConnected: true}})
	}))
	t.Cleanup(server.Close)
	r.APIBase = server.URL
	return r, runner, j
}

func TestNodeUpdateRollbackAndLostCommitResponse(t *testing.T) {
	for _, scenario := range []string{"success", "apply-fails", "commit-response-lost", "ssh-lost", "already-current"} {
		t.Run(scenario, func(t *testing.T) {
			r, runner, j := updateFixture(t)
			target := "remnawave/node@sha256:" + strings.Repeat("b", 64)
			if scenario == "already-current" {
				target = j.Image
			}
			phase := "missing"
			runner.response = func(call string, in []byte) ([]byte, error) {
				payload := string(in)
				if strings.Contains(call, j.Secret) || strings.Contains(call, j.RealityKey) || !strings.Contains(call, "StrictHostKeyChecking=yes") {
					t.Fatal("unsafe SSH update")
				}
				switch {
				case strings.Contains(payload, "RM_UPDATE_ACTION='prepare'"):
					phase = "prepared"
				case strings.Contains(payload, "RM_UPDATE_ACTION='apply'"):
					phase = "trial"
					if scenario == "apply-fails" || scenario == "ssh-lost" {
						return nil, errors.New("lost SSH")
					}
				case strings.Contains(payload, "RM_UPDATE_ACTION='commit'"):
					phase = "committed"
					if scenario == "commit-response-lost" {
						return nil, errors.New("lost commit response")
					}
				case strings.Contains(payload, "RM_UPDATE_ACTION='rollback'"):
					phase = "rolled-back"
				case strings.Contains(payload, "RM_UPDATE_ACTION='status'"):
					if scenario == "ssh-lost" {
						return nil, errors.New("server unreachable")
					}
				default:
					t.Fatal("unexpected SSH request")
				}
				return []byte("RM_UPDATE_PHASE=" + phase + "\nRM_UPDATE_IMAGE=" + target + "\n"), nil
			}
			err := r.updateNode(context.Background(), &j)
			if scenario == "success" || scenario == "already-current" {
				if err != nil || j.Image != target || j.UpdateID != "" {
					t.Fatal("update failed", err)
				}
			} else if err == nil {
				t.Fatal("failure was hidden")
			}
			if scenario == "apply-fails" && (j.Image == target || j.UpdateID != "" || phase != "rolled-back") {
				t.Fatal("old image was not restored")
			}
			if scenario == "commit-response-lost" && (j.Image != target || j.UpdateID != "") {
				t.Fatal("lost acknowledgement undid committed update")
			}
			if scenario == "ssh-lost" && j.UpdateID == "" {
				t.Fatal("lost recovery journal")
			}
			saved, e := r.loadNodeJob(j.ID)
			if e != nil || saved.Secret != j.Secret || saved.RealityKey != j.RealityKey || saved.ShortID != j.ShortID {
				t.Fatal("update changed credentials", e)
			}
		})
	}
}

func TestNodeUpdateOwnershipAndRecoveryGuards(t *testing.T) {
	r, _, j := updateFixture(t)
	if _, err := r.installedNodeJob(testNodeUUID); err != nil {
		t.Fatal(err)
	}
	j.Ready = false
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	if _, err := r.installedNodeJob(testNodeUUID); err == nil {
		t.Fatal("updated incomplete installation")
	}
	j.Ready = true
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	if _, err := r.manageNodeUpdate(context.Background(), services.NodeRequest{Action: "node-update-run"}, services.Node{UUID: j.NodeUUID, Address: j.Params.Address, IsDisabled: true}); err == nil {
		t.Fatal("updated disabled node")
	}
	if _, err := r.manageNodeUpdate(context.Background(), services.NodeRequest{Action: "node-update-run"}, services.Node{UUID: j.NodeUUID, Address: "198.51.100.99", IsConnected: true}); err == nil {
		t.Fatal("updated moved node")
	}
}
