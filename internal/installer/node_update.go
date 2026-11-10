package installer

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

//go:embed node_update_agent.sh
var nodeUpdateAgent []byte

var nodeImagePattern = regexp.MustCompile(`^remnawave/node@sha256:[a-f0-9]{64}$`)

func (r *Real) installedNodeJob(uuid string) (NodeJob, error) {
	entries, err := os.ReadDir(filepath.Join(r.Root, "node-jobs"))
	if err != nil {
		return NodeJob{}, err
	}
	var found *NodeJob
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		j, e := r.loadNodeJob(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			return NodeJob{}, e
		}
		if j.NodeUUID == uuid {
			if found != nil {
				return NodeJob{}, errors.New("multiple journals refer to this node")
			}
			found = &j
		}
	}
	if found == nil || !found.Ready || !nodeImagePattern.MatchString(found.Image) {
		return NodeJob{}, errors.New("updates require a completed node installation managed by this Go version")
	}
	return *found, nil
}

func (r *Real) manageNodeUpdate(ctx context.Context, request services.NodeRequest, node services.Node) (services.NodeResult, error) {
	j, err := r.installedNodeJob(node.UUID)
	if err != nil {
		return services.NodeResult{}, err
	}
	status := jobStatus(j)
	result := services.NodeResult{Install: &status}
	if node.Address != j.Params.Address {
		return result, errors.New("panel node address differs from the saved installation")
	}
	reinstall := strings.HasPrefix(request.Action, "node-reinstall-")
	switch request.Action {
	case "node-update-prepare", "node-reinstall-prepare":
		if j.UpdateID == "" && (node.IsDisabled || !reinstall && !node.IsConnected) {
			return result, errors.New("enable the node; updates also require an active panel connection")
		}
		result, err = r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-rescan", JobID: j.ID})
		result.Text = j.Image
		if j.UpdateID != "" {
			result.Text += "\nRecovery of an interrupted update is required."
			result.TextRU = j.Image + "\nТребуется восстановление прерванного обновления."
		}
		return result, err
	case "node-update-auth", "node-reinstall-auth":
		return r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID, Fingerprint: request.Fingerprint})
	case "node-update-run", "node-reinstall-run":
		if j.UpdateID != "" {
			err = r.recoverNodeUpdate(ctx, &j)
		} else {
			if node.IsDisabled || !reinstall && !node.IsConnected {
				return result, errors.New("enable the node; updates also require an active panel connection")
			}
			err = r.replaceNode(ctx, &j, reinstall)
		}
		result.Text = "Node operation completed. Current image: " + j.Image
		result.TextRU = "Операция завершена. Текущий образ: " + j.Image
		return result, err
	}
	return result, errors.New("unknown node update operation")
}

func (r *Real) runNodeUpdateAgent(ctx context.Context, j NodeJob, action string) (string, string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(j.UpdateID) {
		return "", "", errors.New("invalid update transaction ID")
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(j.ID) || !nodeImagePattern.MatchString(j.Image) {
		return "", "", errors.New("invalid node update parameters")
	}
	switch action {
	case "prepare", "apply", "commit", "rollback", "status":
	default:
		return "", "", errors.New("invalid update action")
	}
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return "", "", err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	mode := "0"
	if j.UpdateReinstall {
		mode = "1"
	}
	input := fmt.Sprintf("set +x\nRM_JOB_ID='%s'\nRM_UPDATE_ID='%s'\nRM_UPDATE_ACTION='%s'\nRM_PREVIOUS_IMAGE='%s'\nRM_REINSTALL='%s'\n", j.ID, j.UpdateID, action, j.Image, mode)
	data, err := r.Runner.Run(ctx, append([]byte(input), nodeUpdateAgent...), "ssh", args...)
	if err != nil {
		return "", "", fmt.Errorf("remote node update %s failed; inspect /var/lib/remnamanager-node/install.log: %w", action, err)
	}
	phase, image := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "RM_UPDATE_PHASE=") {
			phase = strings.TrimSpace(strings.TrimPrefix(line, "RM_UPDATE_PHASE="))
		}
		if strings.HasPrefix(line, "RM_UPDATE_IMAGE=") {
			image = strings.TrimSpace(strings.TrimPrefix(line, "RM_UPDATE_IMAGE="))
		}
	}
	if phase == "missing" && action == "status" {
		return phase, "", nil
	}
	if !nodeImagePattern.MatchString(image) {
		return "", "", errors.New("remote update did not return a pinned official image")
	}
	return phase, image, nil
}

func (r *Real) recoverNodeUpdate(ctx context.Context, j *NodeJob) error {
	phase, image, err := r.runNodeUpdateAgent(ctx, *j, "status")
	if err != nil {
		return err
	}
	if phase == "trial" || phase == "prepared" {
		phase, image, err = r.runNodeUpdateAgent(ctx, *j, "rollback")
		if err != nil {
			return err
		}
	}
	switch phase {
	case "committed":
		if j.UpdateTarget != "" && j.UpdateTarget != image {
			return errors.New("remote committed image differs from the saved update target")
		}
		j.Image = image
	case "rolled-back":
		if err = r.waitInstalledNode(ctx, *j); err != nil {
			return fmt.Errorf("old Compose restored, but panel connectivity is not confirmed: %w", err)
		}
	case "missing": // No remote transaction was prepared; no Compose was changed.
		if j.UpdateTarget != "" {
			return errors.New("remote update journal is missing after preparation; recovery requires inspection")
		}
	default:
		return errors.New("unknown remote update state; recovery journal retained")
	}
	j.UpdateID, j.UpdateTarget = "", ""
	j.UpdateReinstall = false
	return r.saveNodeJob(j)
}

func (r *Real) updateNode(ctx context.Context, j *NodeJob) error {
	return r.replaceNode(ctx, j, false)
}

func (r *Real) replaceNode(ctx context.Context, j *NodeJob, reinstall bool) error {
	id, err := secret()
	if err != nil {
		return err
	}
	j.UpdateID = id[:32]
	j.UpdateReinstall = reinstall
	if err = r.saveNodeJob(j); err != nil {
		return err
	}
	phase, image, err := r.runNodeUpdateAgent(ctx, *j, "prepare")
	if err != nil {
		return r.failNodeUpdate(j, err)
	}
	if phase != "prepared" {
		return r.failNodeUpdate(j, errors.New("remote update was not prepared"))
	}
	if reinstall && image != j.Image {
		return r.failNodeUpdate(j, errors.New("reinstallation must preserve the pinned image"))
	}
	j.UpdateTarget = image
	if err = r.saveNodeJob(j); err != nil {
		return r.failNodeUpdate(j, err)
	}
	if image == j.Image && !reinstall {
		return r.recoverNodeUpdate(ctx, j)
	}
	phase, applied, err := r.runNodeUpdateAgent(ctx, *j, "apply")
	if err != nil || phase != "trial" || applied != image {
		if err == nil {
			err = errors.New("remote update trial did not start")
		}
		return r.failNodeUpdate(j, err)
	}
	// Give the old backend connection time to close before checking the new one.
	select {
	case <-ctx.Done():
		return r.failNodeUpdate(j, ctx.Err())
	case <-time.After(3 * time.Second):
	}
	if err = r.waitInstalledNode(ctx, *j); err != nil {
		return r.failNodeUpdate(j, err)
	}
	phase, confirmed, err := r.runNodeUpdateAgent(ctx, *j, "commit")
	if err != nil || phase != "committed" || confirmed != image {
		if err == nil {
			err = errors.New("remote update commit was not verified")
		}
		return r.failNodeUpdate(j, err)
	}
	j.Image, j.UpdateID, j.UpdateTarget = image, "", ""
	j.UpdateReinstall = false
	return r.saveNodeJob(j)
}

func (r *Real) failNodeUpdate(j *NodeJob, cause error) error {
	// Cancellation of the UI must not cancel recovery. A remote timer remains
	// armed if this separate SSH attempt cannot reach the server.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := r.recoverNodeUpdate(ctx, j); err != nil {
		return fmt.Errorf("%w; recovery incomplete: %v; retry Update node to recover", cause, err)
	}
	return fmt.Errorf("%w; update transaction recovered; current image: %s", cause, j.Image)
}
