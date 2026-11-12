package installer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

//go:embed node_site_agent.sh
var nodeSiteAgent []byte

func (r *Real) runNodeSiteAgent(ctx context.Context, j NodeJob, action, name string) (string, error) {
	if _, err := r.jobPath(j.ID); err != nil {
		return "", err
	}
	if action != "apply" && action != "commit" && action != "rollback" && action != "status" {
		return "", errors.New("invalid website action")
	}
	archive := []byte(nil)
	if action == "apply" {
		found := false
		for _, template := range NodeSiteTemplates {
			found = found || name == template
		}
		if !found {
			return "", errors.New("choose studio, atlas or journal")
		}
		var err error
		archive, err = nodeWebsiteArchive(name)
		if err != nil {
			return "", err
		}
	}
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return "", err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	input := fmt.Sprintf("RM_JOB_ID='%s'\nRM_SITE_ACTION='%s'\nRM_SITE_NAME='%s'\nRM_SITE_B64='%s'\n", j.ID, action, name, base64.StdEncoding.EncodeToString(archive))
	data, err := r.Runner.Run(ctx, append([]byte(input), nodeSiteAgent...), "ssh", args...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "RM_SITE_PHASE=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "RM_SITE_PHASE=")), nil
		}
	}
	return "", errors.New("website agent did not return transaction state")
}

func (r *Real) manageNodeSite(ctx context.Context, request services.NodeRequest, node services.Node) (services.NodeResult, error) {
	j, err := r.installedNodeJob(node.UUID)
	if err != nil {
		return services.NodeResult{}, err
	}
	if j.Version < 2 {
		return services.NodeResult{}, errors.New("website templates require an acme.sh node installation")
	}
	if j.CoreUpdateID != "" || j.UpdateID != "" || j.FirewallPending || node.Address != j.Params.Address {
		return services.NodeResult{}, errors.New("finish node recovery before changing its website")
	}
	switch request.Action {
	case "node-site-auth":
		return r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID})
	case "node-site-run":
		if j.Params.SSH.IdentityFile == "" {
			if err := r.installNodeManagedKey(ctx, &j); err != nil {
				return services.NodeResult{}, err
			}
		}
		if err := r.ensureNodeLayout(ctx, &j); err != nil {
			return services.NodeResult{}, err
		}
		phase, err := r.runNodeSiteAgent(ctx, j, "status", "")
		if err != nil {
			return services.NodeResult{}, err
		}
		if phase == "trial" {
			if _, err := r.runNodeSiteAgent(ctx, j, "rollback", ""); err != nil {
				return services.NodeResult{}, err
			}
		}
		phase, err = r.runNodeSiteAgent(ctx, j, "apply", request.Value)
		if err == nil && phase != "trial" {
			err = errors.New("website trial did not start")
		}
		if err == nil {
			err = r.waitNodeWebsite(ctx, j, request.Value)
		}
		if err != nil {
			recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, rollbackErr := r.runNodeSiteAgent(recovery, j, "rollback", "")
			return services.NodeResult{}, errors.Join(err, rollbackErr)
		}
		phase, err = r.runNodeSiteAgent(ctx, j, "commit", "")
		if err != nil {
			return services.NodeResult{}, fmt.Errorf("website commit needs recovery: %w", err)
		}
		if phase != "committed" {
			return services.NodeResult{}, errors.New("website was not committed")
		}
		j.Params.SiteTemplate = request.Value
		if err := r.saveNodeJob(&j); err != nil {
			return services.NodeResult{}, err
		}
		return services.NodeResult{Text: "Website template changed: " + request.Value, TextRU: "Шаблон сайта изменён: " + request.Value}, nil
	}
	return services.NodeResult{}, errors.New("unknown website operation")
}
