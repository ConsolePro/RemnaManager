package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeReinstallPreservesPinnedImageAndKeys(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-start"}[failed], func(t *testing.T) {
			r, runner, j := updateFixture(t)
			original := j
			phase := "missing"
			applied := false
			runner.response = func(_ string, in []byte) ([]byte, error) {
				payload := string(in)
				if !strings.Contains(payload, "RM_REINSTALL='1'") {
					t.Fatal("lost reinstallation mode")
				}
				switch {
				case strings.Contains(payload, "RM_UPDATE_ACTION='prepare'"):
					phase = "prepared"
				case strings.Contains(payload, "RM_UPDATE_ACTION='apply'"):
					applied = true
					phase = "trial"
					if failed {
						return nil, errors.New("container failed to start")
					}
				case strings.Contains(payload, "RM_UPDATE_ACTION='commit'"):
					phase = "committed"
				case strings.Contains(payload, "RM_UPDATE_ACTION='rollback'"):
					phase = "rolled-back"
				}
				return []byte("RM_UPDATE_PHASE=" + phase + "\nRM_UPDATE_IMAGE=" + j.Image + "\n"), nil
			}
			err := r.replaceNode(context.Background(), &j, true)
			if (err != nil) != failed || !applied {
				t.Fatal("reinstallation skipped recreation or lost error", err)
			}
			saved, e := r.loadNodeJob(j.ID)
			if e != nil || saved.Image != original.Image || saved.Secret != original.Secret || saved.RealityKey != original.RealityKey || saved.ShortID != original.ShortID || saved.NodeUUID != original.NodeUUID || saved.UpdateID != "" || saved.UpdateReinstall {
				t.Fatal("reinstallation changed state", e)
			}
		})
	}
}

func TestNodeReinstallOfflineAndInterruptedRecovery(t *testing.T) {
	r, runner, j := updateFixture(t)
	// Reinstall may repair an offline node. Preparation still scans the pinned
	// SSH key; unlike an update it does not require a live panel connection.
	runner.response = func(_ string, _ []byte) ([]byte, error) { return nil, errors.New("SSH_SCAN_REACHED") }
	node := services.Node{UUID: j.NodeUUID, Address: j.Params.Address}
	_, err := r.manageNodeUpdate(context.Background(), services.NodeRequest{Action: "node-reinstall-prepare"}, node)
	if err == nil || !strings.Contains(err.Error(), "SSH_SCAN_REACHED") {
		t.Fatal("offline reinstall was blocked before SSH", err)
	}
	if _, err := r.manageNodeUpdate(context.Background(), services.NodeRequest{Action: "node-update-prepare"}, node); err == nil || strings.Contains(err.Error(), "SSH_SCAN_REACHED") {
		t.Fatal("ordinary update accepted an offline node")
	}
	j.UpdateID = strings.Repeat("f", 32)
	j.UpdateTarget = j.Image
	j.UpdateReinstall = true
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	runner.response = func(_ string, in []byte) ([]byte, error) {
		if !strings.Contains(string(in), "RM_UPDATE_ACTION='status'") {
			t.Fatal("started another operation instead of recovering")
		}
		return []byte("RM_UPDATE_PHASE=committed\nRM_UPDATE_IMAGE=" + j.Image + "\n"), nil
	}
	// Either menu entry can reconcile a committed operation after a lost reply.
	if _, err := r.manageNodeUpdate(context.Background(), services.NodeRequest{Action: "node-update-run"}, node); err != nil {
		t.Fatal(err)
	}
	saved, err := r.loadNodeJob(j.ID)
	if err != nil || saved.UpdateID != "" || saved.UpdateReinstall {
		t.Fatal("committed reinstall was not reconciled", err)
	}
}
