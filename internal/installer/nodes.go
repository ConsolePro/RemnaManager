package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

var nodeUUIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func ValidateNodeName(name string) error {
	if strings.TrimSpace(name) != name || utf8.RuneCountInString(name) < 3 || utf8.RuneCountInString(name) > 30 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return services.LocalizedError("Имя ноды должно содержать от 3 до 30 символов, без управляющих символов и пробелов по краям.", "node name must contain 3–30 characters without control characters or surrounding spaces")
	}
	return nil
}

func (r *Real) ManageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return services.NodeResult{}, services.LocalizedError("Для управления нодами необходим Linux и запуск от root.", "node management requires Linux and root")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result, err := r.manageNodes(ctx, request)
	if err != nil && strings.HasPrefix(request.Action, "node-warp-") && services.ErrorText(err, "ru") == err.Error() {
		err = services.WrapLocalized(err, "Не удалось настроить WARP. Проверьте доступ ноды по SSH и журнал /opt/remnanode/warp/install.log. Настройку можно повторить.", err.Error())
	}
	if err != nil && strings.HasPrefix(request.Action, "node-core-") && services.ErrorText(err, "ru") == err.Error() {
		err = services.WrapLocalized(err, "Не удалось выполнить операцию с ядром Xray. Проверьте подключение ноды и журнал менеджера: "+r.LogPath, err.Error())
	}
	return result, err
}

func (r *Real) nodeAPI(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	return apiResponse(ctx, r.Client, r.APIBase, r.state.Params.PanelDomain, method, path, r.state.ManagerToken, body)
}

func (r *Real) manageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	var result services.NodeResult
	if err := r.prepareManagement(ctx); err != nil {
		return result, err
	}
	readOnly := request.Action == "nodes-list" || request.Action == "node-details" || request.Action == "node-status" || request.Action == "node-ssh-settings" || request.Action == "node-install-pending" || request.Action == "node-install-load"
	if !readOnly {
		if err := nodeReadinessError(r.state); err != nil {
			return result, err
		}
	}
	if strings.HasPrefix(request.Action, "node-install-") {
		result, err := r.nodeProvision(ctx, request)
		if err != nil {
			return result, fmt.Errorf("%w; private log: %s", err, r.LogPath)
		}
		return result, nil
	}
	if request.Action == "nodes-list" {
		raw, err := r.nodeAPI(ctx, "GET", "/nodes", nil)
		if err != nil {
			return result, err
		}
		// Backend versions expose either a bare array or an object with nodes.
		var nodes []services.Node
		if err = json.Unmarshal(raw, &nodes); err != nil {
			var wrapper struct {
				Nodes []services.Node `json:"nodes"`
			}
			if err = json.Unmarshal(raw, &wrapper); err != nil || wrapper.Nodes == nil {
				return result, errors.New("unexpected node list response")
			}
			nodes = wrapper.Nodes
		}
		if len(nodes) > 10000 {
			return result, errors.New("node list exceeds the supported size")
		}
		for i := range nodes {
			if !nodeUUIDPattern.MatchString(nodes[i].UUID) {
				return result, errors.New("panel returned an invalid node UUID")
			}
			nodes[i].Name = r.safeOutput([]byte(nodes[i].Name))
			nodes[i].Address = r.safeOutput([]byte(nodes[i].Address))
		}
		sort.SliceStable(nodes, func(i, j int) bool {
			if nodes[i].Name == nodes[j].Name {
				return nodes[i].UUID < nodes[j].UUID
			}
			return nodes[i].Name < nodes[j].Name
		})
		result.Nodes = nodes
		return result, nil
	}
	if !nodeUUIDPattern.MatchString(request.UUID) {
		return result, errors.New("invalid node UUID")
	}
	raw, err := r.nodeAPI(ctx, "GET", "/nodes/"+request.UUID, nil)
	if err != nil {
		return result, err
	}
	var node services.Node
	if err = json.Unmarshal(raw, &node); err != nil || !strings.EqualFold(node.UUID, request.UUID) {
		return result, errors.New("unexpected node detail response")
	}
	request.UUID = node.UUID
	if strings.HasPrefix(request.Action, "node-warp-") {
		return r.manageNodeWarp(ctx, request, node)
	}
	node.Name = r.safeOutput([]byte(node.Name))
	node.Address = r.safeOutput([]byte(node.Address))
	node.XrayVersion = r.safeOutput([]byte(node.XrayVersion))
	node.NodeVersion = r.safeOutput([]byte(node.NodeVersion))
	if node.Versions != nil {
		node.Versions.Xray = r.safeOutput([]byte(node.Versions.Xray))
		node.Versions.Node = r.safeOutput([]byte(node.Versions.Node))
	}
	if strings.HasPrefix(request.Action, "node-core-") {
		return r.manageNodeCore(ctx, request, node)
	}
	if strings.HasPrefix(request.Action, "node-site-") {
		return r.manageNodeSite(ctx, request, node)
	}
	if request.Action == "node-ssh-auth" {
		j, err := r.installedNodeJob(node.UUID)
		if err != nil {
			result.NeedsSSHSettings = true
			result.SSH = r.state.NodeSSH[node.UUID]
			if result.SSH.Host == "" {
				result.SSH.Host, result.SSH.Port = node.Address, 22
			}
			result.SSH.HostKey = ""
			return result, nil
		}
		return r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID})
	}
	if request.Action == "node-ssh-open" {
		j, err := r.installedNodeJob(node.UUID)
		if err != nil {
			return result, err
		}
		if err := r.installNodeManagedKey(ctx, &j); err != nil {
			return result, err
		}
		args, err := r.provisionSSHArgs(j, false)
		if err != nil {
			return result, err
		}
		args = append(args, "-t", "root@"+j.Params.SSH.Host)
		result.Command = append([]string{"ssh"}, args...)
		return result, nil
	}
	if strings.HasPrefix(request.Action, "node-update-") || strings.HasPrefix(request.Action, "node-reinstall-") {
		return r.manageNodeUpdate(ctx, request, node)
	}
	switch request.Action {
	case "node-details":
		result.Nodes = []services.Node{node}
	case "node-delete":
		if request.Value != node.UUID {
			return result, errors.New("confirm the exact node UUID before removal")
		}
		_, err = r.nodeAPI(ctx, "DELETE", "/nodes/"+node.UUID, nil)
		if err == nil {
			delete(r.state.NodeSSH, node.UUID)
			err = saveState(statePath(r.Root), r.state)
			result.Text = "Node removed from the panel."
			result.TextRU = "Нода удалена из панели."
		}
	case "node-status":
		state, stateRU := "offline", "не в сети"
		if node.IsDisabled {
			state, stateRU = "disabled", "отключена в панели"
		} else if node.IsConnected {
			state, stateRU = "online", "в сети"
		}
		result.Text = fmt.Sprintf("%s\nUUID: %s\nAddress: %s\nNode API port: %d\nStatus: %s", r.safeOutput([]byte(node.Name)), node.UUID, r.safeOutput([]byte(node.Address)), node.Port, state)
		result.TextRU = fmt.Sprintf("%s\nUUID: %s\nАдрес: %s\nПорт API ноды: %d\nСтатус: %s", r.safeOutput([]byte(node.Name)), node.UUID, r.safeOutput([]byte(node.Address)), node.Port, stateRU)
		var details map[string]json.RawMessage
		json.Unmarshal(raw, &details)
		for _, field := range []struct{ key, ru, en string }{{"nodeVersion", "Версия ноды", "Node version"}, {"xrayVersion", "Версия Xray", "Xray version"}, {"uptime", "Аптайм", "Uptime"}, {"lastStatusChange", "Изменение статуса", "Last status change"}, {"lastMessage", "Сообщение ноды", "Node message"}} {
			value := details[field.key]
			if len(value) == 0 || string(value) == "null" {
				continue
			}
			var text string
			if json.Unmarshal(value, &text) != nil {
				text = string(value)
			}
			text = r.safeOutput([]byte(text))
			result.Text += "\n" + field.en + ": " + text
			result.TextRU += "\n" + field.ru + ": " + text
		}
		result.Text += "\n\nStatus reported by the panel. Remote container logs require SSH support."
		result.TextRU += "\n\nДанные получены из панели. Удалённые логи контейнера требуют SSH-управления."
	case "node-rename":
		if err = ValidateNodeName(request.Value); err != nil {
			return result, err
		}
		_, err = r.nodeAPI(ctx, "PATCH", "/nodes", map[string]string{"uuid": node.UUID, "name": request.Value})
		if err == nil {
			result.Text = "Node renamed to " + request.Value
			result.TextRU = "Нода переименована: " + request.Value
		}
	case "node-toggle":
		if request.Value != "enable" && request.Value != "disable" {
			return result, errors.New("select an explicit node enable/disable action")
		}
		if (request.Value == "disable") == node.IsDisabled {
			result.Text = "Node already has the requested state; no changes made."
			result.TextRU = "Нода уже находится в выбранном состоянии. Изменения не потребовались."
			return result, nil
		}
		_, err = r.nodeAPI(ctx, "POST", "/nodes/"+node.UUID+"/actions/"+request.Value, nil)
		if err == nil {
			result.Text = "Panel node state changed. This operation controls panel communication; it does not stop the remote container."
			result.TextRU = "Состояние ноды в панели изменено. Операция управляет связью с панелью; контейнер на удалённом сервере не останавливается."
		}
	case "node-restart":
		if node.IsDisabled || !node.IsConnected {
			return result, errors.New("enable the node and restore its connection before requesting an Xray restart")
		}
		_, err = r.nodeAPI(ctx, "POST", "/nodes/"+node.UUID+"/actions/restart", map[string]bool{"forceRestart": true})
		if err == nil {
			result.Text = "The panel accepted the Xray restart request. Check node status afterwards."
			result.TextRU = "Панель приняла запрос перезапуска Xray. Проверьте статус ноды после выполнения."
		}
	case "node-ssh-settings":
		result.SSH = r.state.NodeSSH[node.UUID]
		if result.SSH.Host == "" {
			result.SSH.Host = node.Address
			result.SSH.Port = 22
		}
		result.SSH.HostKey = ""
	case "node-ssh-prepare":
		result, err = r.prepareNodeSSH(ctx, node.UUID, request.SSH)
	case "node-ssh-trust":
		result, err = r.trustNodeSSH(ctx, node.UUID, request.SSH, request.Fingerprint)
	default:
		err = errors.New("unknown node operation")
	}
	if err != nil {
		return services.NodeResult{}, fmt.Errorf("%w; private log: %s", err, r.LogPath)
	}
	return result, nil
}

func nodeReadinessError(s State) error {
	switch {
	case s.FirewallPending:
		return services.LocalizedError("Добавление и изменение нод заблокировано: не подтверждён SSH-доступ после установки панели. Откройте «Панель и компоненты → Установить панель», продолжите установку с сохранёнными параметрами и подтвердите доступ по SSH в конце.", "Node changes are blocked: SSH access after panel installation has not been confirmed. Resume Panel and components → Install panel with the saved parameters and confirm SSH access at the end.")
	case s.RecoveryBackup != "":
		return services.LocalizedError("Добавление и изменение нод заблокировано: обновление панели прервано, требуется проверка и восстановление базы данных. Резервная копия: "+s.RecoveryBackup, "Node changes are blocked: a panel update was interrupted; inspect and recover the database. Backup: "+s.RecoveryBackup)
	case s.SubRecovery != "":
		return services.LocalizedError("Добавление и изменение нод заблокировано: не завершено восстановление страницы подписки. Выберите «Панель и компоненты → Страница подписки → Установка / запуск».", "Node changes are blocked: subscription recovery is pending. Choose Panel and components → Subscription page → Install / start.")
	case s.ProtectionPending:
		return services.LocalizedError("Добавление и изменение нод заблокировано: не завершена настройка защиты панели. Откройте «Панель и компоненты → Защита панели» и повторите настройку с тем же режимом и доменом.", "Node changes are blocked: panel protection setup is pending. Open Panel and components → Panel protection and resume with the same mode and domain.")
	case !s.Ready:
		return services.LocalizedError("Добавление и изменение нод заблокировано: установка панели не отмечена как завершённая. Откройте «Панель и компоненты → Установить панель», продолжите с сохранёнными параметрами и подтвердите SSH-доступ в конце. Это состояние панели, а не ошибка в полях ноды.", "Node changes are blocked: panel installation is not marked complete. Resume Panel and components → Install panel with saved parameters and confirm SSH access at the end. This is panel state, not invalid node fields.")
	}
	return nil
}
