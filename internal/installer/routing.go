package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/routing"
	"github.com/ConsolePro/RemnaManager/internal/services"
)

type routingJob struct {
	Version                                            int
	ID, Phase, Name, TemplateUUID, BaseHash, HostsHash string
	Spec                                               services.RoutingSpec
	Config                                             json.RawMessage
	InitialConfig                                      json.RawMessage
}

var routingIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func jsonHash(value any) string {
	data, _ := json.Marshal(value)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var canonical any
	if decoder.Decode(&canonical) != nil {
		return ""
	}
	data, _ = json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (r *Real) ManageRouting(ctx context.Context, request services.RoutingRequest) (services.RoutingResult, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return services.RoutingResult{}, services.LocalizedError("Для маршрутизации требуется Linux и запуск от root.", "Routing management requires Linux and root.")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result, err := r.manageRouting(ctx, request)
	if err != nil {
		ru := services.ErrorText(err, "ru")
		if ru == err.Error() {
			if r.log != nil {
				_, _ = fmt.Fprintf(r.log, "Routing operation %s failed: %v\n", request.Action, err)
			}
			ru = "Проверьте доступ к API панели и разрешения менеджера. Журнал: " + r.LogPath
		}
		return result, services.WrapLocalized(err, "Не удалось выполнить операцию с шаблоном Xray: "+ru, "Xray template operation failed: "+err.Error())
	}
	return result, nil
}

func (r *Real) routingTemplates(ctx context.Context) ([]services.RoutingTemplate, error) {
	objects, err := r.nodeObjects(ctx, "/subscription-templates", "templates")
	if err != nil {
		return nil, err
	}
	result := []services.RoutingTemplate{}
	for _, obj := range objects {
		data, _ := json.Marshal(obj)
		var item services.RoutingTemplate
		if json.Unmarshal(data, &item) != nil || !nodeUUIDPattern.MatchString(item.UUID) {
			return nil, services.LocalizedError("Панель вернула некорректный список шаблонов.", "Panel returned an invalid template collection.")
		}
		if item.Type == "XRAY_JSON" {
			item.Name = r.safeOutput([]byte(item.Name))
			item.Config = nil
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *Real) routingHosts(ctx context.Context) ([]services.RoutingHost, error) {
	objects, err := r.nodeObjects(ctx, "/hosts", "hosts")
	if err != nil {
		return nil, err
	}
	items := []services.RoutingHost{}
	for _, obj := range objects {
		data, _ := json.Marshal(obj)
		var item services.RoutingHost
		if json.Unmarshal(data, &item) != nil || !nodeUUIDPattern.MatchString(item.UUID) {
			return nil, services.LocalizedError("Панель вернула некорректный список подключений.", "Panel returned an invalid host collection.")
		}
		item.Remark = r.safeOutput([]byte(item.Remark))
		item.Address = r.safeOutput([]byte(item.Address))
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UUID < items[j].UUID })
	return items, nil
}

func (r *Real) routingTemplate(ctx context.Context, id string) (services.RoutingTemplate, error) {
	var item services.RoutingTemplate
	if !nodeUUIDPattern.MatchString(id) {
		return item, services.LocalizedError("Некорректный UUID шаблона.", "Invalid template UUID.")
	}
	raw, err := r.nodeAPI(ctx, "GET", "/subscription-templates/"+id, nil)
	if err != nil {
		return item, err
	}
	if json.Unmarshal(raw, &item) != nil || item.UUID != id || item.Type != "XRAY_JSON" || len(item.Config) == 0 {
		return item, services.LocalizedError("Выбранный объект не является шаблоном XRAY_JSON с содержимым.", "Selected object is not an XRAY_JSON template with content.")
	}
	return item, nil
}

func routingHostsHash(spec services.RoutingSpec, hosts []services.RoutingHost) (string, error) {
	byID := map[string]services.RoutingHost{}
	for _, host := range hosts {
		byID[host.UUID] = host
	}
	selected := []services.RoutingHost{}
	for _, pool := range spec.Pools {
		for _, id := range pool.Hosts {
			host, ok := byID[id]
			if !ok || host.Disabled {
				return "", services.LocalizedError("Выбранное подключение удалено или отключено. Обновите схему.", "A selected host was removed or disabled. Refresh the scheme.")
			}
			selected = append(selected, host)
		}
	}
	return jsonHash(selected), nil
}

func (r *Real) saveRoutingJob(j routingJob) error {
	if !routingIDPattern.MatchString(j.ID) {
		return services.LocalizedError("Некорректный идентификатор схемы.", "Invalid scheme ID.")
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return services.LocalizedError("Журнал схемы слишком большой. Сократите исходный шаблон или правила.", "Scheme journal is too large; reduce the base template or rules.")
	}
	dir := filepath.Join(r.Root, "routing")
	if err := noSymlinkParents(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, j.ID+".json"), data, 0600)
}

func (r *Real) loadRoutingJob(id string) (routingJob, error) {
	var job routingJob
	if !routingIDPattern.MatchString(id) {
		return job, services.LocalizedError("Некорректный идентификатор схемы.", "Invalid scheme ID.")
	}
	path := filepath.Join(r.Root, "routing", id+".json")
	if err := noSymlinkParents(path); err != nil {
		return job, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return job, err
	}
	if len(data) > 8<<20 || json.Unmarshal(data, &job) != nil || job.ID != id || job.Version != 1 || !json.Valid(job.Config) || routing.Validate(job.Spec) != nil || job.Name != "RM_"+strings.ReplaceAll(job.Spec.Name, " ", "_")+"_"+id {
		return job, services.LocalizedError("Журнал схемы повреждён. Автоматическое изменение шаблона запрещено.", "Scheme journal is invalid; refusing to modify a template.")
	}
	if job.TemplateUUID != "" && !nodeUUIDPattern.MatchString(job.TemplateUUID) {
		return job, services.LocalizedError("Некорректный UUID сохранённого шаблона.", "Invalid saved template UUID.")
	}
	// A nil RawMessage is serialized as JSON null in a prepared journal.
	// It is still an absent snapshot after loading that journal.
	if bytes.Equal(bytes.TrimSpace(job.InitialConfig), []byte("null")) {
		job.InitialConfig = nil
	}
	switch job.Phase {
	case "prepared", "creating", "configuring", "ready", "removing", "removed":
	default:
		return job, services.LocalizedError("Неизвестный этап схемы.", "Unknown scheme phase.")
	}
	return job, nil
}

func schemeStatus(j routingJob) services.RoutingScheme {
	copy := j.Spec
	copy.Exits = append([]services.RoutingExit(nil), j.Spec.Exits...)
	for i := range copy.Exits {
		copy.Exits[i].URL = ""
		if wg := copy.Exits[i].WireGuard; wg != nil {
			value := *wg
			value.SecretKey = ""
			copy.Exits[i].WireGuard = &value
		}
	}
	return services.RoutingScheme{ID: j.ID, Name: j.Spec.Name, Phase: j.Phase, TemplateUUID: j.TemplateUUID, Spec: copy}
}

func (r *Real) manageRouting(ctx context.Context, request services.RoutingRequest) (services.RoutingResult, error) {
	var result services.RoutingResult
	if err := r.prepareManagement(ctx); err != nil {
		return result, err
	}
	if request.Action != "route-inventory" && request.Action != "route-list" && request.Action != "route-quick-inventory" {
		if err := nodeReadinessError(r.state); err != nil {
			return result, err
		}
	}
	if strings.HasPrefix(request.Action, "route-quick-") {
		return r.manageQuickRouting(ctx, request)
	}
	switch request.Action {
	case "route-inventory":
		hosts, err := r.routingHosts(ctx)
		if err != nil {
			return result, err
		}
		result.Hosts = hosts
		templates, err := r.routingTemplates(ctx)
		if err != nil {
			return result, err
		}
		result.Templates = templates
		return result, nil
	case "route-list":
		entries, err := os.ReadDir(filepath.Join(r.Root, "routing"))
		if os.IsNotExist(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			j, err := r.loadRoutingJob(strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				return result, err
			}
			if j.Phase != "removed" {
				result.Schemes = append(result.Schemes, schemeStatus(j))
			}
		}
		return result, nil
	case "route-preview":
		if err := routing.Validate(request.Spec); err != nil {
			return result, err
		}
		base, err := r.routingTemplate(ctx, request.Spec.BaseTemplateUUID)
		if err != nil {
			return result, err
		}
		hosts, err := r.routingHosts(ctx)
		if err != nil {
			return result, err
		}
		hostsHash, err := routingHostsHash(request.Spec, hosts)
		if err != nil {
			return result, err
		}
		config, err := routing.Render(request.Spec, base.Config)
		if err != nil {
			return result, err
		}
		id, err := secret()
		if err != nil {
			return result, err
		}
		id = id[:32]
		job := routingJob{Version: 1, ID: id, Phase: "prepared", Name: "RM_" + strings.ReplaceAll(request.Spec.Name, " ", "_") + "_" + id, Spec: request.Spec, Config: config, BaseHash: jsonHash(base.Config), HostsHash: hostsHash}
		if err := r.saveRoutingJob(job); err != nil {
			return result, err
		}
		status := schemeStatus(job)
		result.Scheme = &status
		result.TextRU = "Будет создан отдельный клиентский шаблон XRAY_JSON. Исходный шаблон и настройки нод сохраняются.\nГруппы: " + routing.Summary(job.Spec) + "\nПодключения должны быть доступны пользователям в их сквадах."
		result.TextRU += "\nПравила схемы имеют приоритет над правилами исходного шаблона. Сетевые маршруты по умолчанию заменяются выбранным выходом."
		result.Text = "A separate client XRAY_JSON template will be created. The base template and node settings are retained.\nGroups: " + routing.Summary(job.Spec) + "\nSelected connections must be accessible through the users' squads."
		result.Text += "\nScheme rules take priority over base rules. Network defaults are replaced by the selected target."
		return result, nil
	case "route-apply", "route-delete":
		job, err := r.loadRoutingJob(request.ID)
		if err != nil {
			return result, err
		}
		if request.Action == "route-delete" {
			err = r.deleteRoutingTemplate(ctx, &job)
		} else {
			err = r.applyRoutingTemplate(ctx, &job)
		}
		status := schemeStatus(job)
		result.Scheme = &status
		if err != nil {
			return result, services.WrapLocalized(err, services.ErrorText(err, "ru")+"\nСхема сохранена для продолжения. Идентификатор: "+job.ID, err.Error()+"; saved scheme: "+job.ID)
		}
		result.TextRU = "Шаблон: " + job.Name + "\nUUID: " + job.TemplateUUID + "\nНазначьте шаблон нужной записи или группе подписки в панели и обновите подписку в клиенте. Сквады менеджер не меняет."
		result.Text = "Template: " + job.Name + "\nUUID: " + job.TemplateUUID + "\nAssign it to the intended subscription entry or group in the panel and refresh the client subscription. Squads are unchanged."
		if job.Phase == "removed" {
			result.TextRU = "Управляемый шаблон удалён. Подключения и профили нод сохранены."
			result.Text = "Managed template removed. Hosts and node profiles are retained."
		}
		return result, nil
	}
	return result, services.LocalizedError("Неизвестная операция маршрутизации.", "Unknown routing operation.")
}

func (r *Real) applyRoutingTemplate(ctx context.Context, j *routingJob) error {
	if j.Phase == "removing" || j.Phase == "removed" {
		return services.LocalizedError("Эта схема удаляется или уже удалена.", "This scheme is being removed or has been removed.")
	}
	if j.Phase == "ready" {
		current, err := r.routingTemplate(ctx, j.TemplateUUID)
		if err != nil {
			return err
		}
		if current.Name != j.Name || jsonHash(current.Config) != jsonHash(j.Config) {
			return services.LocalizedError("Шаблон изменён через панель. Автоматическая перезапись запрещена.", "Template was edited in the panel; automatic overwrite is refused.")
		}
		return nil
	}
	base, err := r.routingTemplate(ctx, j.Spec.BaseTemplateUUID)
	if err != nil {
		return err
	}
	hosts, err := r.routingHosts(ctx)
	if err != nil {
		return err
	}
	hash, err := routingHostsHash(j.Spec, hosts)
	if err != nil {
		return err
	}
	if jsonHash(base.Config) != j.BaseHash || hash != j.HostsHash {
		return services.LocalizedError("Исходный шаблон или выбранные подключения изменились. Создайте новый предварительный просмотр.", "Base template or selected hosts changed. Prepare a new preview.")
	}
	if j.TemplateUUID == "" {
		templates, err := r.routingTemplates(ctx)
		if err != nil {
			return err
		}
		for _, template := range templates {
			if template.Name == j.Name {
				if j.Phase != "creating" {
					return services.LocalizedError("Такое имя шаблона уже занято. Создайте новую схему.", "Template name already exists; prepare a new scheme.")
				}
				j.TemplateUUID = template.UUID
				break
			}
		}
		if j.TemplateUUID == "" {
			j.Phase = "creating"
			if err := r.saveRoutingJob(*j); err != nil {
				return err
			}
			raw, err := r.nodeAPI(ctx, "POST", "/subscription-templates", map[string]string{"name": j.Name, "templateType": "XRAY_JSON"})
			if err != nil {
				return err
			}
			id, err := apiUUID(raw)
			if err != nil {
				return err
			}
			j.TemplateUUID = id
			var created services.RoutingTemplate
			if json.Unmarshal(raw, &created) == nil && json.Valid(created.Config) {
				j.InitialConfig = created.Config
			}
		}
		j.Phase = "configuring"
		if err := r.saveRoutingJob(*j); err != nil {
			return err
		}
	}
	current, err := r.routingTemplate(ctx, j.TemplateUUID)
	if err != nil {
		return err
	}
	if current.Name != j.Name {
		return services.LocalizedError("Имя шаблона изменилось. Продолжение остановлено.", "Template name changed; refusing to resume.")
	}
	if len(j.InitialConfig) == 0 {
		if err := r.routingTemplateUnused(ctx, j.TemplateUUID, j.Name); err != nil {
			return err
		}
		j.InitialConfig = append(json.RawMessage(nil), current.Config...)
		if err := r.saveRoutingJob(*j); err != nil {
			return err
		}
	}
	if jsonHash(current.Config) != jsonHash(j.Config) {
		if jsonHash(current.Config) != jsonHash(j.InitialConfig) {
			return services.LocalizedError("Новый шаблон изменён вручную. Сохранена исходная копия; автоматическая перезапись остановлена.", "New template was edited manually. The initial snapshot was retained; overwrite is refused.")
		}
		// Only the manager's newly-created shell is changed; never Default.
		if _, err := r.nodeAPI(ctx, "PATCH", "/subscription-templates", map[string]any{"uuid": j.TemplateUUID, "templateJson": json.RawMessage(j.Config)}); err != nil {
			return err
		}
		current, err = r.routingTemplate(ctx, j.TemplateUUID)
		if err != nil {
			return err
		}
		if jsonHash(current.Config) != jsonHash(j.Config) {
			return services.LocalizedError("Панель не подтвердила содержимое нового шаблона.", "Panel did not confirm the new template content.")
		}
	}
	j.Phase = "ready"
	return r.saveRoutingJob(*j)
}

func jsonRefersTo(raw json.RawMessage, values ...string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(item any) bool {
		switch v := item.(type) {
		case string:
			for _, want := range values {
				if want != "" && strings.Contains(v, want) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if visit(child) {
					return true
				}
			}
		case map[string]any:
			for _, child := range v {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func (r *Real) deleteRoutingTemplate(ctx context.Context, j *routingJob) error {
	if j.Phase == "removed" {
		return nil
	}
	if j.TemplateUUID == "" || j.Phase == "removing" {
		list, err := r.routingTemplates(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, item := range list {
			if item.UUID == j.TemplateUUID || j.TemplateUUID == "" && item.Name == j.Name {
				j.TemplateUUID = item.UUID
				found = true
				break
			}
		}
		if !found {
			j.Phase = "removed"
			return r.saveRoutingJob(*j)
		}
	}
	current, err := r.routingTemplate(ctx, j.TemplateUUID)
	if err != nil {
		return err
	}
	if current.Name != j.Name || jsonHash(current.Config) != jsonHash(j.Config) && (len(j.InitialConfig) == 0 || jsonHash(current.Config) != jsonHash(j.InitialConfig)) {
		return services.LocalizedError("Удаление разрешено только для неизменённого шаблона, созданного этой схемой.", "Only an unmodified template created by this scheme may be removed.")
	}
	if err := r.routingTemplateUnused(ctx, j.TemplateUUID, j.Name); err != nil {
		return err
	}
	j.Phase = "removing"
	if err := r.saveRoutingJob(*j); err != nil {
		return err
	}
	if _, err := r.nodeAPI(ctx, "DELETE", "/subscription-templates/"+j.TemplateUUID, nil); err != nil {
		return err
	}
	j.Phase = "removed"
	return r.saveRoutingJob(*j)
}

func (r *Real) routingTemplateUnused(ctx context.Context, id, name string) error {
	for _, path := range []string{"/hosts", "/external-squads", "/subscription-settings"} {
		raw, err := r.nodeAPI(ctx, "GET", path, nil)
		if err != nil {
			return err
		}
		if jsonRefersTo(raw, id, name) {
			return services.LocalizedError("В панели найдены назначения этого шаблона. Сначала снимите назначения и повторите удаление.", "The template is referenced by panel settings. Remove its assignments before deleting it.")
		}
	}
	return nil
}
