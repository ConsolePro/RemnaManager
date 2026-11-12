package installer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/updater"
)

//go:embed node_core_agent.sh
var nodeCoreAgent []byte
var coreVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var corePathPattern = regexp.MustCompile(`^/opt/remnanode/xray/v[0-9]+\.[0-9]+\.[0-9]+/(amd64|arm64)/xray$`)

type coreAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type coreState struct{ Phase, Version, Path string }

func (r *Real) manageNodeCore(ctx context.Context, request services.NodeRequest, node services.Node) (services.NodeResult, error) {
	j, err := r.installedNodeJob(node.UUID)
	if err != nil {
		return services.NodeResult{}, err
	}
	if j.Version < 2 || j.UpdateID != "" || j.FirewallPending || node.Address != j.Params.Address {
		return services.NodeResult{}, errors.New("finish node recovery before updating Xray")
	}
	result := services.NodeResult{}
	rollback := strings.HasPrefix(request.Action, "node-core-rollback-")
	action := strings.Replace(request.Action, "node-core-rollback-", "node-core-", 1)
	unified := action == "node-core-check"
	if unified {
		action = "node-core-prepare"
	}
	switch action {
	case "node-core-prepare":
		if j.CoreUpdateID != "" {
			result.Version = j.CoreTargetVersion
			result.TextRU = "Восстановление прерванного обновления Xray: " + j.CoreTargetVersion
			result.Text = "Recover interrupted Xray update: " + j.CoreTargetVersion
			return result, nil
		}
		if node.IsDisabled || !node.IsConnected {
			return result, errors.New("enable the node and restore its panel connection before updating Xray")
		}
		current, err := currentCoreVersion(node)
		if err != nil {
			return result, err
		}
		catalog := updater.Catalog{Client: r.Client, Repo: "XTLS/Xray-core"}
		if rollback {
			releases, err := catalog.List(ctx)
			if err != nil {
				return result, err
			}
			for _, release := range releases {
				if updater.Newer(current, release.Version) {
					result.Versions = append(result.Versions, release.Version)
				}
			}
			result.Text = "Current Xray: " + current
			result.TextRU = "Текущее ядро Xray: " + current
			if len(result.Versions) == 0 {
				result.NoChange = true
				result.Text += "\nNo older stable versions found in the recent official releases."
				result.TextRU += "\nВ последних официальных релизах нет более ранних стабильных версий."
			}
			return result, nil
		}
		release, err := (updater.Catalog{Client: r.Client, Repo: "XTLS/Xray-core"}).Get(ctx, "latest")
		if err != nil {
			return result, err
		}
		if !updater.Newer(release.Version, current) {
			result = coreUpToDate(current, release.Version)
			if unified {
				releases, err := catalog.List(ctx)
				if err != nil {
					return services.NodeResult{}, err
				}
				for _, previous := range releases {
					if updater.Newer(current, previous.Version) {
						result.Versions = append(result.Versions, previous.Version)
					}
				}
				result.NoChange = len(result.Versions) == 0
			}
			return result, nil
		}
		result.Version = release.Version
		result.Text = "Xray " + current + " → " + release.Version
		result.TextRU = "Доступна новая версия Xray: " + release.Version + "\nТекущая версия: " + current
		return result, nil
	case "node-core-auth":
		return r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID})
	case "node-core-run":
		if j.CoreUpdateID == "" {
			if node.IsDisabled || !node.IsConnected {
				return result, errors.New("node must be enabled and connected")
			}
			current, err := currentCoreVersion(node)
			if err != nil {
				return result, err
			}
			if !updater.TagPattern.MatchString(request.Value) {
				return result, errors.New("select an official Xray release version")
			}
			if rollback {
				if !updater.Newer(current, request.Value) {
					return result, services.LocalizedError("Для отката выберите версию ниже текущей.", "Select a version older than the current core for rollback.")
				}
			} else if !updater.Newer(request.Value, current) {
				return coreUpToDate(current, request.Value), nil
			}
		}
		if j.Params.SSH.IdentityFile == "" {
			if err := r.installNodeManagedKey(ctx, &j); err != nil {
				return result, err
			}
		}
		if j.CoreUpdateID != "" {
			err = r.recoverNodeCore(ctx, &j)
		} else {
			if node.IsDisabled || !node.IsConnected {
				return result, errors.New("node must be enabled and connected")
			}
			if err := r.ensureNodeLayout(ctx, &j); err != nil {
				return result, err
			}
			err = r.updateNodeCore(ctx, &j, request.Value)
		}
		result.Text = "Xray operation completed. Version: " + j.CoreVersion
		result.TextRU = "Операция с Xray завершена. Версия: " + j.CoreVersion
		return result, err
	}
	return result, errors.New("unknown Xray operation")
}

func currentCoreVersion(node services.Node) (string, error) {
	version := "v" + strings.TrimPrefix(strings.TrimSpace(node.CoreVersion()), "v")
	if !updater.TagPattern.MatchString(version) {
		return "", services.LocalizedError("Панель не сообщила текущую версию Xray. Обновление отменено; восстановите подключение ноды и повторите проверку.", "The panel did not report the current Xray version. Restore the node connection and check again.")
	}
	return version, nil
}

func coreUpToDate(current, latest string) services.NodeResult {
	return services.NodeResult{NoChange: true,
		Text:   "Xray core is up to date. Current: " + current + "; release: " + latest + ". No changes made.",
		TextRU: "Версия ядра Xray актуальная. Текущая: " + current + "; релиз: " + latest + ". Изменения не требуются."}
}
func (r *Real) runNodeCoreAgent(ctx context.Context, j NodeJob, action string, assets map[string]coreAsset) (coreState, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(j.CoreUpdateID) || !updater.TagPattern.MatchString(j.CoreTargetVersion) {
		return coreState{}, errors.New("invalid Xray transaction")
	}
	switch action {
	case "prepare", "apply", "commit", "rollback", "status":
	default:
		return coreState{}, errors.New("invalid Xray action")
	}
	data, err := json.Marshal(assets)
	if err != nil {
		return coreState{}, err
	}
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return coreState{}, err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	input := fmt.Sprintf("set +x\nRM_JOB_ID='%s'\nRM_CORE_ID='%s'\nRM_CORE_ACTION='%s'\nRM_CORE_VERSION='%s'\nRM_NODE_IMAGE='%s'\nRM_CORE_ASSETS_B64='%s'\n", j.ID, j.CoreUpdateID, action, j.CoreTargetVersion, j.Image, base64.StdEncoding.EncodeToString(data))
	output, err := r.Runner.Run(ctx, append([]byte(input), nodeCoreAgent...), "ssh", args...)
	if err != nil {
		return coreState{}, fmt.Errorf("Xray %s failed; remote log: %s: %w", action, nodeRemoteLog(j), err)
	}
	state := coreState{}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "RM_CORE_PHASE=") {
			state.Phase = strings.TrimSpace(strings.TrimPrefix(line, "RM_CORE_PHASE="))
		}
		if strings.HasPrefix(line, "RM_CORE_VERSION=") {
			state.Version = strings.TrimSpace(strings.TrimPrefix(line, "RM_CORE_VERSION="))
		}
		if strings.HasPrefix(line, "RM_CORE_PATH=") {
			state.Path = strings.TrimSpace(strings.TrimPrefix(line, "RM_CORE_PATH="))
		}
	}
	if state.Phase == "missing" && action == "status" {
		return state, nil
	}
	if state.Version != j.CoreTargetVersion || !corePathPattern.MatchString(state.Path) || !strings.HasPrefix(state.Path, "/opt/remnanode/xray/"+state.Version+"/") {
		return state, errors.New("invalid Xray transaction response")
	}
	return state, nil
}
func (r *Real) recoverNodeCore(ctx context.Context, j *NodeJob) error {
	state, err := r.runNodeCoreAgent(ctx, *j, "status", nil)
	if err != nil {
		return err
	}
	if state.Phase == "trial" || state.Phase == "prepared" {
		state, err = r.runNodeCoreAgent(ctx, *j, "rollback", nil)
		if err != nil {
			return err
		}
	}
	switch state.Phase {
	case "committed":
		j.CoreVersion, j.CoreBinary = state.Version, state.Path
	case "rolled-back":
		if err := r.waitInstalledNode(ctx, *j); err != nil {
			return err
		}
	case "missing": // Remote apply always records trial before editing Compose.
	default:
		return errors.New("unknown Xray recovery state")
	}
	j.CoreUpdateID, j.CoreTargetVersion = "", ""
	return r.saveNodeJob(j)
}
func (r *Real) failNodeCore(j *NodeJob, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := r.recoverNodeCore(ctx, j); err != nil {
		return fmt.Errorf("%w; Xray recovery pending: %v", cause, err)
	}
	return fmt.Errorf("%w; Xray transaction recovered", cause)
}
func (r *Real) updateNodeCore(ctx context.Context, j *NodeJob, version string) error {
	catalog := updater.Catalog{Client: r.Client, Repo: "XTLS/Xray-core"}
	release, err := catalog.Get(ctx, version)
	if err != nil {
		return err
	}
	assets := map[string]coreAsset{}
	for arch, name := range map[string]string{"amd64": "Xray-linux-64.zip", "arm64": "Xray-linux-arm64-v8a.zip"} {
		asset, err := catalog.Asset(release, name)
		if err != nil {
			return err
		}
		digest, err := catalog.Digest(ctx, release, asset)
		if err != nil {
			return err
		}
		assets[arch] = coreAsset{URL: asset.URL, SHA256: digest}
	}
	id, err := secret()
	if err != nil {
		return err
	}
	j.CoreUpdateID, j.CoreTargetVersion = id[:32], release.Version
	if err = r.saveNodeJob(j); err != nil {
		return err
	}
	state, err := r.runNodeCoreAgent(ctx, *j, "prepare", assets)
	if err != nil {
		return r.failNodeCore(j, err)
	}
	if state.Phase != "prepared" {
		return r.failNodeCore(j, errors.New("Xray preparation was not confirmed"))
	}
	state, err = r.runNodeCoreAgent(ctx, *j, "apply", nil)
	if err != nil || state.Phase != "trial" {
		if err == nil {
			err = errors.New("Xray trial did not start")
		}
		return r.failNodeCore(j, err)
	}
	if err = r.waitNodeCore(ctx, *j); err != nil {
		return r.failNodeCore(j, err)
	}
	if err = r.waitNodeWebsite(ctx, *j); err != nil {
		return r.failNodeCore(j, err)
	}
	state, err = r.runNodeCoreAgent(ctx, *j, "commit", nil)
	if err != nil || state.Phase != "committed" {
		if err == nil {
			err = errors.New("Xray commit not confirmed")
		}
		return r.failNodeCore(j, err)
	}
	j.CoreVersion, j.CoreBinary = state.Version, state.Path
	j.CoreUpdateID, j.CoreTargetVersion = "", ""
	return r.saveNodeJob(j)
}
func (r *Real) waitNodeCore(ctx context.Context, j NodeJob) error {
	wait, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	for {
		raw, err := r.nodeAPI(wait, "GET", "/nodes/"+j.NodeUUID, nil)
		if err == nil {
			var node services.Node
			if json.Unmarshal(raw, &node) == nil && node.IsConnected && !node.IsDisabled && strings.TrimPrefix(node.CoreVersion(), "v") == strings.TrimPrefix(j.CoreTargetVersion, "v") {
				return nil
			}
		}
		select {
		case <-wait.Done():
			return errors.New("panel did not confirm the selected running Xray version")
		case <-time.After(2 * time.Second):
		}
	}
}
