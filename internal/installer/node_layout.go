package installer

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed node_layout_agent.sh
var nodeLayoutAgent []byte

// Migrate only completed acme.sh installations, under the remote agent lock.
func (r *Real) ensureNodeLayout(ctx context.Context, j *NodeJob) error {
	if j.Version >= 4 || j.LayoutVersion == 1 {
		return nil
	}
	if j.Version < 2 {
		return errors.New("legacy certbot node layout requires a separate migration")
	}
	if j.CoreUpdateID != "" || j.UpdateID != "" || j.FirewallPending {
		return errors.New("finish node recovery before migrating its files")
	}
	args, err := r.provisionSSHArgs(*j, true)
	if err != nil {
		return err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	input := fmt.Sprintf("RM_JOB_ID='%s'\n", j.ID)
	data, err := r.Runner.Run(ctx, append([]byte(input), nodeLayoutAgent...), "ssh", args...)
	if err != nil {
		return fmt.Errorf("node layout migration failed: %w", err)
	}
	if !strings.Contains(string(data), "RM_NODE_LAYOUT=1") {
		return errors.New("node layout migration did not confirm completion")
	}
	j.LayoutVersion = 1
	return r.saveNodeJob(j)
}
