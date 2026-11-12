package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/routing"
	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Quick routing changes the selected existing subscription template in place.
// The original JSON is retained independently of the node installation state.
type quickRoutingJob struct {
	ID, TemplateUUID, Name, Phase, HostsHash string
	Created                                  int64
	Nodes                                    []string
	Before, Previous, After                  json.RawMessage
}

func (r *Real) quickNodes(ctx context.Context) ([]services.Node, error) {
	objects, err := r.nodeObjects(ctx, "/nodes", "nodes")
	if err != nil {
		return nil, err
	}
	nodes := []services.Node{}
	for _, object := range objects {
		data, _ := json.Marshal(object)
		var node services.Node
		if json.Unmarshal(data, &node) != nil || !nodeUUIDPattern.MatchString(node.UUID) {
			return nil, services.LocalizedError("Панель вернула неверный список нод.", "Invalid panel node list.")
		}
		node.Name = r.safeOutput([]byte(node.Name))
		node.Address = r.safeOutput([]byte(node.Address))
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes, nil
}

func (r *Real) quickRoutingHosts(ctx context.Context, selected []string) ([]string, string, error) {
	if len(selected) == 0 || len(selected) > 64 {
		return nil, "", services.LocalizedError("Отметьте от 1 до 64 нод пробелом.", "Select 1–64 nodes with Space.")
	}
	nodes, err := r.quickNodes(ctx)
	if err != nil {
		return nil, "", err
	}
	hosts, err := r.routingHosts(ctx)
	if err != nil {
		return nil, "", err
	}
	ids, seen := []string{}, map[string]bool{}
	snapshot := []services.RoutingHost{}
	selectedSeen := map[string]bool{}
	for _, id := range selected {
		if !nodeUUIDPattern.MatchString(id) || selectedSeen[id] {
			return nil, "", services.LocalizedError("Некорректный выбор нод.", "Invalid node selection.")
		}
		selectedSeen[id] = true
		var node *services.Node
		for i := range nodes {
			if nodes[i].UUID == id {
				node = &nodes[i]
				break
			}
		}
		if node == nil || node.IsDisabled {
			return nil, "", services.LocalizedError("Выбранная нода удалена или отключена в панели.", "Selected node is missing or disabled.")
		}
		profile := ""
		if node.ConfigProfile != nil {
			profile = node.ConfigProfile.UUID
		}
		if profile == "" {
			if job, err := r.installedNodeJob(id); err == nil {
				profile = job.ProfileUUID
			}
		}
		found := false
		for _, host := range hosts {
			matches := false
			for _, owner := range host.Nodes {
				matches = matches || owner == id
			}
			if len(host.Nodes) == 0 {
				matches = profile != "" && profile == host.Inbound.Profile
			}
			if host.Disabled || !matches {
				continue
			}
			found = true
			if !seen[host.UUID] {
				seen[host.UUID] = true
				ids = append(ids, host.UUID)
				snapshot = append(snapshot, host)
			}
		}
		if !found {
			return nil, "", services.LocalizedError("У ноды «"+node.Name+"» нет включённых хостов подписки. Проверьте привязку хоста к ноде в панели.", "Node "+node.Name+" has no enabled subscription hosts. Check panel host bindings.")
		}
	}
	if len(ids) > 64 {
		return nil, "", services.LocalizedError("У выбранных нод больше 64 хостов. Выберите меньше нод.", "Selected nodes have more than 64 hosts; select fewer nodes.")
	}
	return ids, jsonHash(snapshot), nil
}

func (r *Real) saveQuickRouting(j quickRoutingJob) error {
	if !routingIDPattern.MatchString(j.ID) || !nodeUUIDPattern.MatchString(j.TemplateUUID) {
		return services.LocalizedError("Некорректный журнал маршрутизации.", "Invalid routing journal.")
	}
	path := filepath.Join(r.Root, "quick-routing", j.ID+".json")
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return services.LocalizedError("Шаблон слишком большой для резервной копии.", "Template backup is too large.")
	}
	return writeFile(path, data, 0600)
}

func (r *Real) loadQuickRouting(id string) (quickRoutingJob, error) {
	var j quickRoutingJob
	if !routingIDPattern.MatchString(id) {
		return j, services.LocalizedError("Неверный идентификатор операции.", "Invalid routing operation ID.")
	}
	path := filepath.Join(r.Root, "quick-routing", id+".json")
	if err := noSymlinkParents(path); err != nil {
		return j, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if len(data) > 16<<20 || json.Unmarshal(data, &j) != nil || j.ID != id || !nodeUUIDPattern.MatchString(j.TemplateUUID) || !json.Valid(j.Before) || !json.Valid(j.Previous) || !json.Valid(j.After) || (j.Phase != "prepared" && j.Phase != "applied") {
		return j, services.LocalizedError("Резервная копия маршрутизации повреждена.", "Invalid routing backup.")
	}
	return j, nil
}

func (r *Real) quickRoutingBase(current services.RoutingTemplate) (json.RawMessage, error) {
	entries, err := os.ReadDir(filepath.Join(r.Root, "quick-routing"))
	if os.IsNotExist(err) {
		return current.Config, nil
	}
	if err != nil {
		return nil, err
	}
	var latest quickRoutingJob
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		j, err := r.loadQuickRouting(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if j.TemplateUUID == current.UUID && jsonHash(j.After) == jsonHash(current.Config) && j.Created > latest.Created {
			latest = j
		}
	}
	if latest.ID != "" {
		return latest.Before, nil
	}
	return current.Config, nil
}

func (r *Real) manageQuickRouting(ctx context.Context, req services.RoutingRequest) (services.RoutingResult, error) {
	var result services.RoutingResult
	switch req.Action {
	case "route-quick-inventory":
		var err error
		result.Templates, err = r.routingTemplates(ctx)
		if err != nil {
			return result, err
		}
		result.Nodes, err = r.quickNodes(ctx)
		return result, err
	case "route-quick-preview":
		current, err := r.routingTemplate(ctx, req.Spec.BaseTemplateUUID)
		if err != nil {
			return result, err
		}
		hosts, hash, err := r.quickRoutingHosts(ctx, req.Nodes)
		if err != nil {
			return result, err
		}
		base, err := r.quickRoutingBase(current)
		if err != nil {
			return result, err
		}
		spec := services.RoutingSpec{Name: "Quick_route", BaseTemplateUUID: current.UUID, DefaultTarget: "main", Pools: []services.RoutingPool{{Name: "main", Hosts: hosts}}, Rules: []services.RoutingRule{{Target: "direct", Domains: []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru"}, IPs: []string{"geoip:ru"}}}}
		after, err := routing.Render(spec, base)
		if err != nil {
			return result, err
		}
		id, err := secret()
		if err != nil {
			return result, err
		}
		j := quickRoutingJob{ID: id[:32], TemplateUUID: current.UUID, Name: current.Name, Phase: "prepared", Created: time.Now().UnixNano(), Before: base, Previous: current.Config, After: after, Nodes: req.Nodes, HostsHash: hash}
		if err := r.saveQuickRouting(j); err != nil {
			return result, err
		}
		result.Scheme = &services.RoutingScheme{ID: j.ID, TemplateUUID: j.TemplateUUID, Name: current.Name, Phase: j.Phase}
		result.TextRU = "Изменить существующий шаблон «" + r.safeOutput([]byte(current.Name)) + "»?\nРФ — напрямую. Остальной трафик — через отмеченные ноды; при нескольких нодах выбирается доступная с меньшей задержкой.\nИзменение действует на всех получателей этого шаблона. Новый шаблон и сквады не создаются. Исходный JSON сохранён в резервной копии.\nКлиенту нужны базы geoip:ru и geosite:category-ru."
		result.Text = "Update existing template " + r.safeOutput([]byte(current.Name)) + "?\nRussia goes direct; other traffic uses the selected nodes, choosing the available node with the lowest latency.\nAffects all recipients of this template. No new template or squads. Original JSON is backed up.\nThe client needs geoip:ru and geosite:category-ru assets."
		return result, nil
	case "route-quick-apply":
		j, err := r.loadQuickRouting(req.ID)
		if err != nil {
			return result, err
		}
		current, err := r.routingTemplate(ctx, j.TemplateUUID)
		if err != nil {
			return result, err
		}
		if jsonHash(current.Config) != jsonHash(j.After) {
			if jsonHash(current.Config) != jsonHash(j.Previous) {
				return result, services.LocalizedError("Шаблон изменился после выбора. Откройте настройку заново; ручные изменения сохранены.", "Template changed; reopen setup. Manual edits were retained.")
			}
			_, hash, err := r.quickRoutingHosts(ctx, j.Nodes)
			if err != nil {
				return result, err
			}
			if hash != j.HostsHash {
				return result, services.LocalizedError("Настройки хостов выбранных нод изменились. Повторите выбор.", "Selected node hosts changed; select again.")
			}
			if _, err := r.nodeAPI(ctx, "PATCH", "/subscription-templates", map[string]any{"uuid": j.TemplateUUID, "templateJson": j.After}); err != nil {
				return result, err
			}
			current, err = r.routingTemplate(ctx, j.TemplateUUID)
			if err != nil {
				return result, err
			}
			if jsonHash(current.Config) != jsonHash(j.After) {
				return result, services.LocalizedError("Панель не подтвердила маршрутизацию. Повторите применение.", "Panel did not confirm routing; retry applying.")
			}
		}
		j.Phase = "applied"
		if err := r.saveQuickRouting(j); err != nil {
			return result, err
		}
		result.TextRU = "Маршрутизация применена к существующему шаблону «" + r.safeOutput([]byte(current.Name)) + "».\nОбновите подписку XRAY_JSON в клиенте. Обычная VLESS-ссылка эти правила не передаёт."
		result.Text = "Routing applied to existing template " + r.safeOutput([]byte(current.Name)) + ".\nRefresh the XRAY_JSON subscription in the client. A plain VLESS URI cannot carry these rules."
		return result, nil
	}
	return result, services.LocalizedError("Неизвестная операция маршрутизации.", "Unknown routing operation.")
}
