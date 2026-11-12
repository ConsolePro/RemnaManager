package installer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/updater"
)

//go:embed node_warp_agent.sh
var nodeWarpAgent []byte

const nativeWarpTag = "rm-native-warp"

var nativeWarpDomains = []string{"domain:whoer.net", "domain:browserleaks.com", "domain:2ip.io", "domain:2ip.ru"}

type nodeWarpJob struct {
	NodeUUID, ProfileUUID, Phase string
	Before, After                json.RawMessage
	RemoveFrom, Removal          json.RawMessage
}

func (r *Real) warpJournalPath(id string) (string, error) {
	if !nodeUUIDPattern.MatchString(id) {
		return "", services.LocalizedError("Неверный UUID ноды WARP.", "Invalid WARP node UUID.")
	}
	path := filepath.Join(r.Root, "warp-nodes", id+".json")
	return path, noSymlinkParents(path)
}
func (r *Real) saveWarpJournal(j nodeWarpJob) error {
	path, err := r.warpJournalPath(j.NodeUUID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 12<<20 {
		return services.LocalizedError("Профиль Xray слишком большой.", "Xray profile is too large.")
	}
	return writeFile(path, data, 0600)
}
func (r *Real) loadWarpJournal(id string) (nodeWarpJob, error) {
	var j nodeWarpJob
	path, err := r.warpJournalPath(id)
	if err != nil {
		return j, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if len(data) > 12<<20 || json.Unmarshal(data, &j) != nil || j.NodeUUID != id || !nodeUUIDPattern.MatchString(j.ProfileUUID) || !json.Valid(j.Before) || !json.Valid(j.After) {
		return j, services.LocalizedError("Журнал WARP повреждён.", "Invalid WARP journal.")
	}
	switch j.Phase {
	case "prepared", "installed", "removing", "removed":
	default:
		return j, services.LocalizedError("Неизвестное состояние WARP.", "Unknown WARP phase.")
	}
	return j, nil
}

func (r *Real) warpProfile(ctx context.Context, j NodeJob) (json.RawMessage, string, error) {
	raw, err := r.nodeAPI(ctx, "GET", "/config-profiles/"+j.ProfileUUID, nil)
	if err != nil {
		return nil, "", err
	}
	var profile struct {
		UUID     string          `json:"uuid"`
		Config   json.RawMessage `json:"config"`
		Inbounds []struct {
			UUID string `json:"uuid"`
			Tag  string `json:"tag"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(raw, &profile) != nil || profile.UUID != j.ProfileUUID || !json.Valid(profile.Config) {
		return nil, "", services.LocalizedError("Панель не вернула профиль Xray ноды.", "Panel did not return the node Xray profile.")
	}
	tag := ""
	for _, in := range profile.Inbounds {
		if in.UUID == j.InboundUUID {
			tag = in.Tag
		}
	}
	if tag == "" {
		return nil, "", services.LocalizedError("Не найден сохранённый inbound ноды. Профиль не изменён.", "Saved node inbound was not found. Profile unchanged.")
	}
	return profile.Config, tag, nil
}

func nativeWarpConfig(before json.RawMessage, inbound string) (json.RawMessage, error) {
	var config map[string]any
	if len(before) > 4<<20 || json.Unmarshal(before, &config) != nil || config == nil {
		return nil, services.LocalizedError("Некорректный серверный профиль Xray.", "Invalid server Xray profile.")
	}
	outs, ok := config["outbounds"].([]any)
	if !ok {
		return nil, services.LocalizedError("В профиле нет outbounds.", "Profile has no outbounds.")
	}
	for _, v := range outs {
		out, _ := v.(map[string]any)
		if out["tag"] == nativeWarpTag {
			return nil, services.LocalizedError("В профиле уже есть сторонний выход rm-native-warp. Автоматическая замена запрещена.", "A foreign rm-native-warp outbound already exists; overwrite refused.")
		}
	}
	config["outbounds"] = append(outs, map[string]any{"tag": nativeWarpTag, "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIPv4"}, "streamSettings": map[string]any{"sockopt": map[string]any{"interface": "rmwarp", "mark": 51888}}})
	route, ok := config["routing"].(map[string]any)
	if !ok && config["routing"] != nil {
		return nil, services.LocalizedError("Некорректная секция routing.", "Invalid routing section.")
	}
	if route == nil {
		route = map[string]any{}
	}
	rules, ok := route["rules"].([]any)
	if !ok && route["rules"] != nil {
		return nil, services.LocalizedError("Некорректные правила routing.", "Invalid routing rules.")
	}
	rule := map[string]any{"type": "field", "inboundTag": []string{inbound}, "domain": nativeWarpDomains, "outboundTag": nativeWarpTag}
	route["rules"] = append([]any{rule}, rules...)
	config["routing"] = route
	return json.MarshalIndent(config, "", "  ")
}

func removeNativeWarpConfig(current json.RawMessage, j nodeWarpJob) (json.RawMessage, error) {
	if jsonHash(current) == jsonHash(j.After) || jsonHash(current) == jsonHash(j.Before) {
		return j.Before, nil
	}
	var config, installed map[string]any
	if json.Unmarshal(current, &config) != nil || json.Unmarshal(j.After, &installed) != nil || config == nil {
		return nil, services.LocalizedError("Некорректный профиль WARP.", "Invalid WARP profile.")
	}
	outs, ok := config["outbounds"].([]any)
	if !ok {
		return nil, services.LocalizedError("Некорректные выходы Xray.", "Invalid Xray outbounds.")
	}
	var expected any
	installedOuts, valid := installed["outbounds"].([]any)
	if !valid {
		return nil, services.LocalizedError("Журнал WARP повреждён: нет сохранённых выходов.", "WARP journal has no saved outbounds.")
	}
	for _, item := range installedOuts {
		out, _ := item.(map[string]any)
		if out["tag"] == nativeWarpTag {
			expected = out
		}
	}
	kept := []any{}
	for _, item := range outs {
		out, _ := item.(map[string]any)
		if out["tag"] == nativeWarpTag {
			if jsonHash(out) != jsonHash(expected) {
				return nil, services.LocalizedError("Выход WARP изменён вручную. Автоматическое удаление остановлено.", "WARP outbound was manually changed; removal stopped.")
			}
			continue
		}
		kept = append(kept, item)
	}
	config["outbounds"] = kept
	if route, ok := config["routing"].(map[string]any); ok {
		// All rules for this manager-owned tag are part of the removed feature,
		// including an operator's custom domain list. Unrelated rules survive.
		if balancers, ok := route["balancers"].([]any); ok {
			for _, value := range balancers {
				if jsonRefersTo(mustJSON(value), nativeWarpTag) {
					return nil, services.LocalizedError("Балансировщик использует выход WARP. Сначала уберите эту ссылку в панели.", "A balancer uses WARP; remove that reference first.")
				}
			}
		}
		if rules, ok := route["rules"].([]any); ok {
			clean := []any{}
			for _, item := range rules {
				rule, _ := item.(map[string]any)
				if rule["outboundTag"] != nativeWarpTag {
					clean = append(clean, item)
				}
			}
			route["rules"] = clean
		}
	}
	return json.MarshalIndent(config, "", "  ")
}

func mustJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

func (r *Real) runNodeWarpAgent(ctx context.Context, j NodeJob, action string, assets map[string]coreAsset, config json.RawMessage) error {
	if action != "install" && action != "remove" && action != "validate" && action != "checkpoint" {
		return services.LocalizedError("Неверная операция WARP.", "Invalid WARP action.")
	}
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	data, _ := json.Marshal(assets)
	prefix := fmt.Sprintf("set +x\nRM_JOB_ID='%s'\nRM_WARP_ACTION='%s'\nRM_WARP_ASSETS='%s'\nRM_WARP_CONFIG='%s'\n", j.ID, action, base64.StdEncoding.EncodeToString(data), base64.StdEncoding.EncodeToString(config))
	out, err := r.Runner.Run(ctx, append([]byte(prefix), nodeWarpAgent...), "ssh", args...)
	if err != nil {
		codes := map[string][2]string{"dependencies": {"Не удалось установить WireGuard и зависимости. Проверьте apt на ноде.", "WireGuard dependency installation failed."}, "register": {"Cloudflare не выдал аккаунт WARP. Проверьте доступ ноды к Cloudflare и повторите установку.", "Cloudflare WARP registration failed; check node connectivity and retry."}, "trace": {"WARP не прошёл сетевую проверку. Профиль Xray не применялся; проверьте UDP-доступ ноды к Cloudflare.", "WARP network probe failed; Xray profile was not applied. Check outbound UDP to Cloudflare."}, "validate": {"Профиль с WARP не прошёл проверку ядром ноды. Рабочий профиль сохранён.", "Node Xray rejected the WARP profile; existing profile retained."}, "foreign": {"На ноде найдены чужие настройки WARP или занятые сетевые правила. Они сохранены.", "Foreign WARP configuration or conflicting routing rules found; preserved."}}
		for code, message := range codes {
			if strings.Contains(string(out), "RM_WARP_FAILURE="+code+"\n") {
				return services.WrapLocalized(err, message[0]+" Журнал: /opt/remnanode/warp/install.log", message[1])
			}
		}
		return services.WrapLocalized(err, "Операция WARP на ноде не завершена. Журнал: /opt/remnanode/warp/install.log. Повторите операцию.", "WARP agent failed; inspect /opt/remnanode/warp/install.log and retry.")
	}
	if !strings.Contains(string(out), "RM_WARP_OK="+action) {
		return services.LocalizedError("Нода не подтвердила операцию WARP.", "Node did not confirm WARP operation.")
	}
	return nil
}

func (r *Real) manageNodeWarp(ctx context.Context, req services.NodeRequest, node services.Node) (services.NodeResult, error) {
	var result services.NodeResult
	j, err := r.installedNodeJob(node.UUID)
	if err != nil {
		return result, err
	}
	if !j.Ready || j.FirewallPending || j.CoreUpdateID != "" || j.UpdateID != "" || j.Params.Address != node.Address {
		return result, services.LocalizedError("Сначала завершите установку или восстановление ноды.", "Complete node installation or recovery first.")
	}
	if node.IsDisabled || !node.IsConnected {
		return result, services.LocalizedError("Включите ноду и восстановите подключение к панели перед настройкой WARP.", "Enable the node and restore panel connectivity before configuring WARP.")
	}
	if node.ConfigProfile != nil && node.ConfigProfile.UUID != j.ProfileUUID {
		return result, services.LocalizedError("Профиль ноды изменён в панели. Восстановите соответствие профиля сохранённой установке.", "Node profile changed in the panel; reconcile it with the saved installation.")
	}
	install := strings.HasPrefix(req.Action, "node-warp-install-")
	if strings.HasSuffix(req.Action, "-auth") {
		return r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID})
	}
	current, inbound, err := r.warpProfile(ctx, j)
	if err != nil {
		return result, err
	}
	w, loadErr := r.loadWarpJournal(node.UUID)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return result, loadErr
	}
	if install {
		nodes, err := r.quickNodes(ctx)
		if err != nil {
			return result, err
		}
		for _, other := range nodes {
			if other.UUID != node.UUID && other.ConfigProfile != nil && other.ConfigProfile.UUID == j.ProfileUUID {
				return result, services.LocalizedError("Профиль используется несколькими нодами. Для установки WARP на одну ноду нужен её отдельный профиль; общий профиль не изменён.", "Profile is shared by multiple nodes. WARP on one node requires its own profile; shared profile unchanged.")
			}
		}
		if loadErr != nil || w.Phase == "removed" {
			after, err := nativeWarpConfig(current, inbound)
			if err != nil {
				return result, err
			}
			w = nodeWarpJob{NodeUUID: node.UUID, ProfileUUID: j.ProfileUUID, Phase: "prepared", Before: current, After: after}
		}
		if w.Phase == "removing" {
			return result, services.LocalizedError("Сначала завершите удаление WARP.", "Complete WARP removal first.")
		}
		if w.ProfileUUID != j.ProfileUUID || (jsonHash(current) != jsonHash(w.Before) && jsonHash(current) != jsonHash(w.After)) {
			return result, services.LocalizedError("Профиль изменён вручную после подготовки WARP. Автоматическая перезапись запрещена.", "Profile was edited after WARP preparation; overwrite refused.")
		}
	} else {
		if os.IsNotExist(loadErr) || w.Phase == "removed" {
			return services.NodeResult{NoChange: true, Text: "WARP is not installed by this manager.", TextRU: "WARP не установлен этим менеджером."}, nil
		}
		if w.ProfileUUID != j.ProfileUUID {
			return result, services.LocalizedError("Профиль изменён вручную. Для сохранения ваших правил автоматическое удаление WARP остановлено.", "Profile was manually edited; automatic WARP removal stopped to retain your rules.")
		}
		if w.Phase == "removing" {
			if !json.Valid(w.Removal) || !json.Valid(w.RemoveFrom) || (jsonHash(current) != jsonHash(w.Removal) && jsonHash(current) != jsonHash(w.RemoveFrom)) {
				return result, services.LocalizedError("Профиль изменился во время удаления WARP. Автоматическая перезапись остановлена.", "Profile changed during WARP removal; overwrite stopped.")
			}
		} else {
			w.Removal, err = removeNativeWarpConfig(current, w)
			if err != nil {
				return result, err
			}
			w.RemoveFrom = current
		}
	}
	if strings.HasSuffix(req.Action, "-prepare") {
		result.TextRU = "На выбранной ноде будет установлен WireGuard WARP. Файлы: /opt/remnanode/warp.\nЧерез WARP пойдут whoer.net, browserleaks.com, 2ip.io и 2ip.ru, как в примере. Остальные правила сохраняются. Выход добавится в существующий серверный профиль Xray.\nБудет зарегистрирован аккаунт Cloudflare с принятием условий WARP (https://www.cloudflare.com/application/terms/). Ключ остаётся на ноде; подпискам он не передаётся.\nКонтейнер ноды может кратковременно перезапуститься."
		result.Text = "Install native WireGuard WARP on this node under /opt/remnanode/warp. Route whoer.net, browserleaks.com, 2ip.io and 2ip.ru through WARP in the existing server Xray profile; retain other rules. Register a Cloudflare account accepting WARP terms (https://www.cloudflare.com/application/terms/). Keys stay on the node. The node container may briefly restart."
		if !install {
			result.TextRU = "Удалить управляемый WARP, его выход и маршруты из профиля Xray? Остальные настройки сохраняются. Аккаунт и резервные копии останутся в /opt/remnanode/warp для повторной установки."
			result.Text = "Remove managed WARP and its outbound/rules, retaining unrelated profile settings? Account and backups remain under /opt/remnanode/warp for reinstallation."
		}
		return result, nil
	}
	if !strings.HasSuffix(req.Action, "-run") {
		return result, services.LocalizedError("Неизвестное действие WARP.", "Unknown WARP action.")
	}
	if j.Params.SSH.IdentityFile == "" {
		if err := r.installNodeManagedKey(ctx, &j); err != nil {
			return result, err
		}
	}
	if err := r.ensureNodeLayout(ctx, &j); err != nil {
		return result, err
	}
	if install {
		if node.IsDisabled || !node.IsConnected {
			return result, services.LocalizedError("Для установки WARP нода должна быть подключена к панели.", "Node must be connected to install WARP.")
		}
		if err := r.saveWarpJournal(w); err != nil {
			return result, err
		}
		catalog := updater.Catalog{Client: r.Client, Repo: "ViRb3/wgcf"}
		release, err := catalog.Get(ctx, "latest")
		if err != nil {
			return result, err
		}
		assets := map[string]coreAsset{}
		for _, arch := range []string{"amd64", "arm64"} {
			asset, err := catalog.Asset(release, "wgcf_"+strings.TrimPrefix(release.Version, "v")+"_linux_"+arch)
			if err != nil {
				return result, err
			}
			digest, err := catalog.Digest(ctx, release, asset)
			if err != nil {
				return result, err
			}
			assets[arch] = coreAsset{URL: asset.URL, SHA256: digest}
		}
		if err := r.runNodeWarpAgent(ctx, j, "install", assets, nil); err != nil {
			return result, err
		}
		j.WarpEnabled = true
		if err := r.saveNodeJob(&j); err != nil {
			return result, err
		}
		if err := r.runNodeWarpAgent(ctx, j, "validate", nil, w.After); err != nil {
			return result, err
		}
		// Recheck after remote installation, which can take several minutes.
		fresh, _, err := r.warpProfile(ctx, j)
		if err != nil {
			return result, err
		}
		if jsonHash(fresh) != jsonHash(w.Before) && jsonHash(fresh) != jsonHash(w.After) {
			return result, services.LocalizedError("Профиль изменился во время установки. WARP установлен, но профиль не перезаписан.", "Profile changed during installation. WARP installed; profile not overwritten.")
		}
		if jsonHash(fresh) != jsonHash(w.After) {
			if _, err := r.nodeAPI(ctx, "PATCH", "/config-profiles", map[string]any{"uuid": j.ProfileUUID, "config": w.After}); err != nil {
				return result, err
			}
		}
		confirmed, _, err := r.warpProfile(ctx, j)
		if err != nil {
			return result, err
		}
		if jsonHash(confirmed) != jsonHash(w.After) {
			return result, services.LocalizedError("Панель не подтвердила профиль WARP. Повторите установку для проверки.", "Panel did not confirm WARP profile; retry installation to verify.")
		}
		if err := r.restartWarpNode(ctx, j); err != nil {
			return result, err
		}
		if err := r.waitNodeWarp(ctx, j); err != nil {
			return result, err
		}
		w.Phase = "installed"
		result.TextRU = "WARP Native установлен на ноде. Сетевая проверка Cloudflare пройдена.\nПроверочные сайты направлены через rm-native-warp в существующем серверном профиле. Список доменов можно изменить в этом правиле панели. Ключи WARP остаются только на ноде."
		result.Text = "WARP Native installed; Cloudflare connectivity verified. Probe sites use rm-native-warp in the existing server profile. Edit that rule's domains in the panel as needed. Keys stay on the node."
	} else {
		w.Phase = "removing"
		if err := r.saveWarpJournal(w); err != nil {
			return result, err
		}
		if jsonHash(current) != jsonHash(w.Removal) {
			if _, err := r.nodeAPI(ctx, "PATCH", "/config-profiles", map[string]any{"uuid": j.ProfileUUID, "config": w.Removal}); err != nil {
				return result, err
			}
		}
		confirmed, _, err := r.warpProfile(ctx, j)
		if err != nil {
			return result, err
		}
		if jsonHash(confirmed) != jsonHash(w.Removal) {
			return result, services.LocalizedError("Исходный профиль не подтверждён. Интерфейс WARP сохранён; повторите удаление.", "Original profile not confirmed. WARP interface retained; retry removal.")
		}
		if err := r.restartWarpNode(ctx, j); err != nil {
			return result, err
		}
		if err := r.waitNodeWarp(ctx, j); err != nil {
			return result, err
		}
		if err := r.runNodeWarpAgent(ctx, j, "remove", nil, nil); err != nil {
			return result, err
		}
		if err := r.waitNodeWarp(ctx, j); err != nil {
			return result, err
		}
		j.WarpEnabled = false
		if err := r.saveNodeJob(&j); err != nil {
			return result, err
		}
		w.Phase = "removed"
		result.TextRU = "WARP и его маршруты удалены. Остальные настройки Xray сохранены. Аккаунт и копии остались на ноде."
		result.Text = "WARP and its routes removed; unrelated Xray settings retained. Account and backups remain on the node."
	}
	return result, r.saveWarpJournal(w)
}

func (r *Real) restartWarpNode(ctx context.Context, j NodeJob) error {
	if err := r.runNodeWarpAgent(ctx, j, "checkpoint", nil, nil); err != nil {
		return err
	}
	_, err := r.nodeAPI(ctx, "POST", "/nodes/"+j.NodeUUID+"/actions/restart", map[string]bool{"forceRestart": true})
	return err
}

func (r *Real) waitNodeWarp(ctx context.Context, j NodeJob) error {
	wait, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		raw, err := r.nodeAPI(wait, "GET", "/nodes/"+j.NodeUUID, nil)
		var node services.Node
		if err == nil && json.Unmarshal(raw, &node) == nil && node.IsConnected && !node.IsDisabled {
			if err := r.runNodeWarpAgent(wait, j, "validate", nil, nil); err == nil {
				return nil
			}
		}
		select {
		case <-wait.Done():
			return services.LocalizedError("Профиль сохранён, но запуск Xray ещё не подтверждён. Повторите операцию WARP для проверки.", "Profile saved, but running Xray is unconfirmed; repeat WARP operation to verify.")
		case <-time.After(2 * time.Second):
		}
	}
}
