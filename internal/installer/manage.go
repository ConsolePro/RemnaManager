package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/x/ansi"
)

func (r *Real) Manage(ctx context.Context, request services.PanelRequest) (services.PanelResult, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return services.PanelResult{}, errors.New("component management requires Linux and root")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manage(ctx, request)
}

func (r *Real) prepareManagement(ctx context.Context, removal ...bool) error {
	allowRemoval := len(removal) > 0 && removal[0]
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := loadState(statePath(r.Root))
	if err != nil {
		return err
	}
	if s.Params.PanelDomain == "" {
		return services.LocalizedError("Сначала установите панель: сохранённое состояние установки не найдено.", "install the panel first; no managed installation state was found")
	}
	if err = ValidateParameters(s.Params); err != nil {
		return err
	}
	marker, err := os.ReadFile(filepath.Join(r.Stack, ".remnamanager-go"))
	if err != nil || string(marker) != "managed\n" {
		if !allowRemoval || (!s.RemovalPending && !r.removablePartialStack(s)) {
			return services.LocalizedError("Не найден маркер принадлежности стека RemnaManager. Изменение чужой установки запрещено.", "the Compose stack is not owned by RemnaManager; refusing to modify it")
		}
	}
	if r.lock == nil {
		r.lock, err = lockFile(filepath.Join(r.Root, "install.lock"))
		if err != nil {
			return services.LocalizedError("Уже выполняется другая операция RemnaManager. Дождитесь её завершения.", "another RemnaManager operation is running")
		}
	}
	// Another process may have completed between the initial read and flock.
	s, err = loadState(statePath(r.Root))
	if err != nil {
		return err
	}
	if err = ValidateParameters(s.Params); err != nil {
		return err
	}
	r.state = s
	if s.RemovalPending && !allowRemoval {
		return services.LocalizedError("Удаление панели было прервано. Выберите «Панель и компоненты → Удалить панель и данные», чтобы завершить очистку.", "panel removal was interrupted; choose Remove panel to finish")
	}
	if r.log == nil {
		r.log, err = os.CreateTemp(r.Root, "management-*.log")
		if err != nil {
			return err
		}
		r.LogPath = r.log.Name()
		if _, ok := r.Runner.(ExecRunner); ok {
			r.Runner = ExecRunner{Log: r.log}
		}
	}
	return nil
}

func (r *Real) manage(ctx context.Context, request services.PanelRequest) (services.PanelResult, error) {
	var result services.PanelResult
	// Reading the saved password must also work after an interrupted install,
	// without Docker, a complete stack or a long-running operation's lock.
	if request.Action == "panel-credentials" {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		s, err := loadState(statePath(r.Root))
		if err != nil {
			return result, err
		}
		if s.Params.PanelDomain == "" {
			return result, errors.New("no saved panel credentials were found")
		}
		result.Text = accessDetails(s, r.Root, false) + "\nCopy in terminal: Ctrl+Shift+C or the context menu."
		result.TextRU = accessDetails(s, r.Root, true) + "\nКопирование в терминале: Ctrl+Shift+C или меню мыши."
		return result, nil
	}
	if request.Action == "panel-remove" && request.Value != "DELETE" {
		return result, errors.New("panel removal requires typing DELETE to confirm permanent data loss")
	}
	if err := r.prepareManagement(ctx, request.Action == "panel-remove"); err != nil {
		return result, err
	}
	if request.Action == "panel-remove" {
		if err := r.removePanel(ctx); err != nil {
			return result, fmt.Errorf("removal incomplete; retry Remove panel: %w; private log: %s", err, r.LogPath)
		}
		return services.PanelResult{
			Text:   "Panel, subscription and authentication data removed. You can install the panel again. Backups, certificates, Docker, proxy packages and RemnaManager were preserved. Remote nodes were not changed.",
			TextRU: "Панель, подписка и данные дополнительного входа удалены. Можно установить панель заново.\nРезервные копии, сертификаты, Docker, пакеты прокси и RemnaManager сохранены. Удалённые ноды не изменены.",
		}, nil
	}
	s := r.state
	var err error
	readOnly := request.Action == "panel-status" || request.Action == "panel-logs" || request.Action == "panel-backup"
	if !readOnly && (!s.Ready || s.FirewallPending) {
		return result, errors.New("complete installation and confirm SSH access before managing components")
	}
	if !readOnly && s.ProtectionPending && request.Action != "panel-protection" {
		return result, errors.New("resume Panel protection before managing components")
	}
	if !readOnly && s.RecoveryBackup != "" {
		return result, fmt.Errorf("a backend update was interrupted; inspect logs and restore the database if needed before clearing RecoveryBackup; backup: %s", s.RecoveryBackup)
	}
	if !readOnly && s.SubRecovery != "" && request.Action != "sub-start" {
		return result, errors.New("subscription update was interrupted; choose Subscription page → Install / start to restore the previous image")
	}
	var data []byte
	switch request.Action {
	case "panel-protection":
		err = r.changeProtection(ctx, request.Protection, request.Value)
		if err == nil {
			result.Text = accessDetails(r.state, r.Root, false)
			result.TextRU = accessDetails(r.state, r.Root, true)
		}
	case "panel-status":
		data, err = r.compose(ctx, "--profile", "subscription", "ps", "--all")
		result.Text = fmt.Sprintf("Backend: %s\nSubscription image: %s\nPanel: https://%s\nSubscription: https://%s\n\n%s", s.BackendTag, s.SubImage, s.Params.PanelDomain, s.Params.SubscriptionDomain, r.safeOutput(data))
		if s.RecoveryBackup != "" {
			result.Text += "\nRecovery required: " + s.RecoveryBackup
		}
		if s.SubRecovery != "" {
			result.Text += "\nSubscription recovery pending: " + s.SubRecovery
		}
		if readiness := nodeReadinessError(s); readiness != nil {
			result.Text += "\n\n" + services.ErrorText(readiness, "en")
		}
	case "panel-logs":
		data, err = r.compose(ctx, append([]string{"--profile", "subscription", "logs", "--tail", "100", "--no-color", "--timestamps"}, r.panelServices()...)...)
		result.Text = r.safeOutput(data)
	case "panel-stop":
		_, err = r.compose(ctx, append([]string{"--profile", "subscription", "stop"}, r.panelServices()...)...)
		result.Text = "Panel, subscription, database and cache stopped. Volumes, proxy, certificates and firewall preserved."
	case "panel-start":
		err = r.startProtection(ctx)
		if err == nil {
			_, err = r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave")
		}
		if err == nil {
			err = r.waitAPI(ctx)
		}
		result.Text = "Panel started. Start the subscription page separately if it was stopped."
	case "panel-backup":
		result.Text, err = r.backup(ctx)
	case "panel-token":
		err = r.changeToken(ctx, request.Value)
		result.Text = "Manager API token validated and saved. The subscription token was not changed; the old manager token was not revoked."
	case "panel-update":
		result.Text, err = r.updatePanel(ctx, request.Value)
	case "sub-start":
		err = r.startSubscription(ctx)
		result.Text = "Subscription page started: https://" + s.Params.SubscriptionDomain + "/<shortUuid>"
	case "sub-stop":
		_, err = r.compose(ctx, "--profile", "subscription", "stop", "subscription")
		result.Text = "Subscription page stopped. Configuration and panel data preserved."
	case "sub-update", "sub-image":
		image := request.Value
		if request.Action == "sub-update" {
			image = s.SubSource
			if image == "" {
				image = subscriptionUpdateSource(s.SubImage)
			}
		}
		result.Text, err = r.updateSubscription(ctx, image)
	case "panel-console":
		result.Command = append([]string{"docker"}, r.composeArgs("exec", "remnawave", "cli")...)
	default:
		err = errors.New("unknown panel operation")
	}
	if err != nil {
		if request.Action != "panel-status" && request.Action != "panel-logs" {
			result.Text = ""
		}
		return result, fmt.Errorf("%w; private log: %s", err, r.LogPath)
	}
	if result.Text == "" && len(result.Command) == 0 {
		result.Text = "Operation complete."
	}
	switch request.Action {
	case "panel-status":
		result.TextRU = fmt.Sprintf("Версия backend: %s\nОбраз подписки: %s\nПанель: https://%s\nПодписка: https://%s\n\n%s", s.BackendTag, s.SubImage, s.Params.PanelDomain, s.Params.SubscriptionDomain, r.safeOutput(data))
		if s.RecoveryBackup != "" {
			result.TextRU += "\nТребуется восстановление БД: " + s.RecoveryBackup
		}
		if s.SubRecovery != "" {
			result.TextRU += "\nТребуется восстановление подписки: " + s.SubRecovery
		}
		if readiness := nodeReadinessError(s); readiness != nil {
			result.TextRU += "\n\n" + services.ErrorText(readiness, "ru")
		}
	case "panel-stop":
		result.TextRU = "Панель, подписка, PostgreSQL и Valkey остановлены.\nДанные, прокси, сертификаты и firewall сохранены."
	case "panel-start":
		result.TextRU = "Панель запущена и готова к работе.\nСтраницу подписки при необходимости запустите отдельно."
	case "panel-backup":
		result.TextRU = "Резервная копия БД и конфигурации:\n" + result.Text
	case "panel-token":
		result.TextRU = "Токен управления проверен и сохранён.\nТокен подписок не изменён. Старый токен управления не отозван."
	case "sub-start":
		result.TextRU = "Страница подписки запущена и проверена:\nhttps://" + s.Params.SubscriptionDomain + "/<shortUuid>"
	case "sub-stop":
		result.TextRU = "Страница подписки остановлена.\nКонфигурация и данные панели сохранены."
	case "sub-update", "sub-image":
		result.TextRU = "Образ подписки обновлён и проверен:\n" + r.state.SubImage
	}
	return result, nil
}

func (r *Real) composeArgs(args ...string) []string {
	return append([]string{"compose", "--project-directory", r.Stack, "-f", filepath.Join(r.Stack, "compose.json")}, args...)
}

func (r *Real) safeOutput(data []byte) string {
	text := ansi.Strip(string(data))
	for _, secret := range []string{r.state.DBPassword, r.state.AppSecret, r.state.MetricsPassword, r.state.WebhookSecret, r.state.AdminPassword, r.state.ManagerToken, r.state.SubToken, r.state.CookieKey, r.state.CookieValue, r.state.TinyPassword, r.state.TinyHash, r.state.MFAPassword, r.state.MFASigningKey} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	text = strings.Map(func(c rune) rune {
		if c == '\n' || c == '\t' || c >= 32 && c != 127 {
			return c
		}
		return -1
	}, text)
	if len(text) > 65536 {
		text = string([]rune(text)[0:min(len([]rune(text)), 16000)]) + "\n[output truncated; see private log]"
	}
	return text
}

func (r *Real) backup(ctx context.Context) (string, error) {
	stream, ok := r.Runner.(StreamRunner)
	if !ok {
		return "", errors.New("runner does not support streaming database backups")
	}
	directory, err := os.MkdirTemp(filepath.Join(r.Root), "backup-"+time.Now().UTC().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(directory, "database.dump.partial"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	err = stream.RunTo(ctx, f, "docker", r.composeArgs("exec", "-T", "remnawave-db", "pg_dump", "-U", "postgres", "-d", "postgres", "-Fc")...)
	if err == nil {
		err = f.Sync()
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("incomplete backup retained at %s: %w", directory, err)
	}
	if statErr != nil {
		return "", statErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if info.Size() < 5 {
		return "", errors.New("database dump is empty")
	}
	file, err := os.Open(filepath.Join(directory, "database.dump.partial"))
	if err != nil {
		return "", err
	}
	header := make([]byte, 5)
	_, err = file.Read(header)
	file.Close()
	if err != nil || string(header) != "PGDMP" {
		return "", errors.New("invalid PostgreSQL custom-format backup")
	}
	if err = os.Rename(filepath.Join(directory, "database.dump.partial"), filepath.Join(directory, "database.dump")); err != nil {
		return "", err
	}
	names := []string{"compose.json", ".env", "subscription.env"}
	if protectionMode(r.state.Params) == "tinyauth" {
		names = append(names, "tinyauth.env")
	}
	if protectionMode(r.state.Params) == "mfa" {
		names = append(names, "mfa.env", "mfa.Caddyfile")
	}
	for _, name := range names {
		data, e := os.ReadFile(filepath.Join(r.Stack, name))
		if e != nil {
			return "", e
		}
		if e = writeFile(filepath.Join(directory, name), data, 0600); e != nil {
			return "", e
		}
	}
	if protectionMode(r.state.Params) == "mfa" {
		if err = r.backupMFAUsers(ctx, directory); err != nil {
			return "", fmt.Errorf("incomplete MFA identity backup retained at %s: %w", directory, err)
		}
	}
	if err = saveState(statePath(directory), r.state); err != nil {
		return "", err
	}
	jobsDir := filepath.Join(r.Root, "node-jobs")
	entries, readErr := os.ReadDir(jobsDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", readErr
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path, checkErr := r.jobPath(strings.TrimSuffix(entry.Name(), ".json"))
		if checkErr != nil {
			return "", checkErr
		}
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			return "", errors.New("cannot back up a non-regular node journal")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return "", e
		}
		if e = writeFile(filepath.Join(directory, "node-jobs", entry.Name()), data, 0600); e != nil {
			return "", e
		}
	}
	if err = writeFile(filepath.Join(directory, "COMPLETE"), []byte("PostgreSQL custom-format dump and private configuration\n"), 0600); err != nil {
		return "", err
	}
	return directory, nil
}

func (r *Real) changeToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if len(token) < 16 || len(token) > 4096 || strings.ContainsAny(token, " \r\n\t") {
		return errors.New("invalid API token")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", r.APIBase+"/nodes", nil)
	if err != nil {
		return err
	}
	request.Host = r.state.Params.PanelDomain
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	response, err := r.Client.Do(request)
	if err != nil {
		return errors.New("cannot validate manager token against the panel API")
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("manager token validation failed: HTTP %d", response.StatusCode)
	}
	old := r.state.ManagerToken
	r.state.ManagerToken = token
	if err := saveState(statePath(r.Root), r.state); err != nil {
		r.state.ManagerToken = old
		return err
	}
	return nil
}

func (r *Real) setComposeImage(service, image string) ([]byte, error) {
	path := filepath.Join(r.Stack, "compose.json")
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(old, &doc); err != nil {
		return nil, err
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid managed Compose services")
	}
	svc, ok := services[service].(map[string]any)
	if !ok {
		return nil, errors.New("managed Compose service is missing")
	}
	svc["image"] = image
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return old, writeFile(path, data, 0600)
}
