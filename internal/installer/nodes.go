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
		return errors.New("node name must contain 3–30 characters without control characters or surrounding spaces")
	}
	return nil
}

func (r *Real) ManageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return services.NodeResult{}, errors.New("node management requires Linux and root")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manageNodes(ctx, request)
}

func (r *Real) nodeAPI(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	return apiResponse(ctx, r.Client, r.APIBase, r.state.Params.PanelDomain, method, path, r.state.ManagerToken, body)
}

func (r *Real) manageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	var result services.NodeResult
	if err := r.prepareManagement(ctx); err != nil {
		return result, err
	}
	readOnly := request.Action == "nodes-list" || request.Action == "node-status" || request.Action == "node-ssh-settings" || request.Action == "node-install-pending" || request.Action == "node-install-load"
	if !readOnly && (!r.state.Ready || r.state.FirewallPending || r.state.RecoveryBackup != "" || r.state.SubRecovery != "") {
		return result, errors.New("finish installation or component recovery before modifying nodes")
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
	switch request.Action {
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
