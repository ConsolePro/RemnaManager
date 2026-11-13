package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func (r *Real) proxyLifecycle(ctx context.Context, action string) error {
	if r.state.ProxyRuntime == "docker" {
		_, err := r.proxyCompose(ctx, r.state.Params.Proxy, action)
		return err
	}
	_, err := r.Runner.Run(ctx, nil, "systemctl", action, r.state.Params.Proxy)
	return err
}

func (r *Real) inspectPanel(ctx context.Context) (services.PanelResult, error) {
	s, err := loadState(statePath(r.Root))
	if err != nil {
		return services.PanelResult{}, err
	}
	r.state = s
	result := services.PanelResult{Installation: "absent", Text: "Not installed", TextRU: "Не установлено"}
	if s.Params.PanelDomain == "" {
		if s.DetachedSubscription != nil && s.DetachedSubscription.RemoteSubscription != nil {
			result.Remote = *s.DetachedSubscription.RemoteSubscription
			result.Text += "\nRemote subscription was preserved; its panel API is no longer available."
			result.TextRU += "\nУдалённая подписка сохранена; API удалённой панели ей больше недоступен."
		}
		entries, e := os.ReadDir(r.Stack)
		if e == nil && len(entries) > 0 && !r.retainedDockerStack() {
			result.Installation = "partial"
			result.Text = "Existing unmanaged files; installation will not overwrite them."
			result.TextRU = "Найдены файлы установки. Автоматическая перезапись запрещена."
		}
		return result, nil
	}
	result.Installation = "partial"
	marker, e := os.ReadFile(filepath.Join(r.Stack, ".remnamanager-go"))
	if e != nil || string(marker) != "managed\n" {
		result.Text = "Partial installation: ownership marker missing"
		result.TextRU = "Частичная установка: отсутствует маркер стека"
		return result, nil
	}
	check, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if _, err = r.compose(check, "config", "--quiet"); err != nil {
		result.Text = "Configuration error / Ошибка конфигурации"
		result.TextRU = result.Text
		return result, nil
	}
	data, err := r.compose(check, "--profile", "subscription", "ps", "--all", "--format", "json")
	if err != nil {
		return result, err
	}
	var rows []struct{ Service, State, Health string }
	if len(data) > 0 && data[0] == '[' {
		err = json.Unmarshal(data, &rows)
	} else {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		for {
			var row struct{ Service, State, Health string }
			e := decoder.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil {
				err = e
				break
			}
			rows = append(rows, row)
		}
	}
	if err != nil {
		return result, errors.New("unreadable Docker status")
	}
	if s.LayoutVersion >= 1 && s.Params.RemoteSubscription == nil {
		subdata, e := r.subscriptionCompose(check, "ps", "--all", "--format", "json")
		if e != nil {
			return result, e
		}
		var extra []struct{ Service, State, Health string }
		if len(subdata) > 0 && subdata[0] == '[' {
			e = json.Unmarshal(subdata, &extra)
		} else {
			decoder := json.NewDecoder(strings.NewReader(string(subdata)))
			for {
				var row struct{ Service, State, Health string }
				e = decoder.Decode(&row)
				if e == io.EOF {
					e = nil
					break
				}
				if e != nil {
					break
				}
				extra = append(extra, row)
			}
		}
		if e != nil {
			return result, e
		}
		rows = append(rows, extra...)
		data = append(data, subdata...)
	}
	required := map[string]bool{}
	for _, name := range r.panelServices() {
		required[name] = true
	}
	if s.LayoutVersion >= 1 && s.Params.RemoteSubscription == nil {
		required["subscription"] = true
	}
	healthy := true
	running := 0
	found := 0
	for _, row := range rows {
		if s.LayoutVersion >= officialLayout && row.Service == "remnawave-subscription-page" {
			row.Service = "subscription"
		}
		if !required[row.Service] {
			continue
		}
		found++
		if row.State == "running" {
			running++
		}
		if row.Health == "unhealthy" {
			healthy = false
		}
	}
	complete := found == len(required)
	if complete && running == 0 {
		result.Installation = "stopped"
		result.Text = "Installed, stopped / Установлено, остановлено"
	} else if complete && running == len(required) && healthy {
		result.Installation = "running"
		result.Text = "Running / Работает"
		if _, e = apiCall(check, r.Client, r.APIBase, s.Params.PanelDomain, "GET", "/auth/status", "", nil); e != nil {
			result.Installation = "unavailable"
			result.Text = "Container running, API unavailable / Контейнер работает, API недоступен"
		}
	} else {
		result.Text = "Partial or unhealthy installation / Частичная установка или сбой сервиса"
	}
	if !s.Ready || s.FirewallPending || s.ProtectionPending || s.RecoveryBackup != "" || s.RemovalPending {
		result.Installation = "partial"
		result.Text += "\nRecovery pending / Требуется завершить восстановление"
	}
	if s.Ready && result.Installation != "running" {
		result.Text += "\nSaved state differs from runtime; data preserved / Состояние отличается от сохранённого, данные сохранены"
	}
	if s.ProxyRuntime == "docker" {
		proxy, e := r.proxyCompose(check, s.Params.Proxy, "ps", "--status", "running", "-q", s.Params.Proxy)
		if e != nil || strings.TrimSpace(string(proxy)) == "" {
			result.Text += "\nProxy is stopped or unavailable / Прокси остановлен или недоступен"
			if result.Installation == "running" {
				result.Installation = "unavailable"
			}
		}
	}
	if s.Params.RemoteSubscription != nil {
		remote, e := r.remoteSubscription(check, "status", "")
		if e != nil {
			result.Text += "\nRemote SSH unavailable / Нет связи с удалённой подпиской"
			result.Installation = "remote-unavailable"
		} else {
			result.Text += "\nRemote subscription:\n" + remote
		}
	}
	result.Text += "\n" + r.safeOutput(data)
	result.TextRU = result.Text
	return result, nil
}

func (r *Real) subscriptionStatus(ctx context.Context) (string, error) {
	id, err := r.compose(ctx, "--profile", "subscription", "ps", "--all", "-q", "subscription")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(id)) == "" {
		return "Not installed / Не установлено", nil
	}
	data, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", `{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}} restarts={{.RestartCount}} image={{.Image}}`, strings.TrimSpace(string(id)))
	return r.safeOutput(data), err
}

func (r *Real) panelVersion(ctx context.Context) (string, error) {
	id, err := r.compose(ctx, "ps", "--all", "-q", "remnawave")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(id)) == "" {
		return "", errors.New("backend container not found")
	}
	data, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", `image={{.Image}} reference={{.Config.Image}}`, strings.TrimSpace(string(id)))
	if err != nil {
		return "", err
	}
	actual, actualErr := r.installedBackendVersion(ctx)
	if actualErr == nil {
		data = append(data, []byte("\nInstalled version: "+actual)...)
	} else {
		data = append(data, []byte("\nInstalled version is not verified; digest/reference shown above.")...)
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/remnawave/backend/releases/latest", nil)
	resp, err := r.Client.Do(request)
	if err != nil {
		return r.safeOutput(data), err
	}
	defer resp.Body.Close()
	var release struct {
		Tag               string `json:"tag_name"`
		Draft, Prerelease bool
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil || release.Draft || release.Prerelease || ValidateBackendVersion(release.Tag) != nil {
		return r.safeOutput(data), errors.New("cannot verify latest stable backend release")
	}
	return fmt.Sprintf("Saved backend: %s\nRunning: %s\nLatest official stable: %s\nhttps://github.com/remnawave/backend/releases/tag/%s", r.state.BackendTag, r.safeOutput(data), release.Tag, release.Tag), nil
}

func (r *Real) installedBackendVersion(ctx context.Context) (string, error) {
	id, err := r.compose(ctx, "ps", "--all", "-q", "remnawave")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(id)) == "" {
		return "", errors.New("backend container missing")
	}
	image, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", "{{.Image}}", strings.TrimSpace(string(id)))
	if err != nil {
		return "", err
	}
	label, err := r.Runner.Run(ctx, nil, "docker", "image", "inspect", "--format", `{{index .Config.Labels "org.opencontainers.image.version"}}`, strings.TrimSpace(string(image)))
	version := strings.TrimPrefix(strings.TrimSpace(string(label)), "v")
	if err == nil && ValidateBackendVersion(version) == nil {
		return version, nil
	}
	reference, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", "{{.Config.Image}}", strings.TrimSpace(string(id)))
	if err != nil {
		return "", err
	}
	version = strings.TrimPrefix(strings.TrimSpace(string(reference)), "remnawave/backend:")
	if ValidateBackendVersion(version) != nil {
		return "", errors.New("cannot determine installed backend version from image metadata; latest is not a version")
	}
	return version, nil
}

func (r *Real) panelDiagnostics(ctx context.Context, asset string) (string, error) {
	rows := []string{"Proxy: " + r.state.Params.Proxy + " (" + r.state.ProxyRuntime + ")"}
	if asset != "" {
		if !strings.HasPrefix(asset, "/assets/") || !strings.HasSuffix(asset, ".wasm") || strings.ContainsAny(asset, "?#\r\n") {
			return "", errors.New("enter the exact /assets/...wasm path from browser Network")
		}
		client := *r.Client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		etag := ""
		for i := 0; i < 2; i++ {
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+r.state.Params.PanelDomain+asset, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			if etag != "" {
				req.Header.Set("If-None-Match", etag)
			}
			if protectionMode(r.state.Params) == "cookie" {
				req.AddCookie(&http.Cookie{Name: r.state.CookieKey, Value: r.state.CookieValue})
			}
			start := time.Now()
			resp, e := client.Do(req)
			if e != nil {
				return strings.Join(rows, "\n"), e
			}
			first := time.Since(start)
			size, e := io.Copy(io.Discard, io.LimitReader(resp.Body, (64<<20)+1))
			resp.Body.Close()
			if e != nil {
				return "", e
			}
			rows = append(rows, fmt.Sprintf("request=%d status=%d protocol=%s type=%s encoding=%s cache=%s etag=%s vary=%s bytes=%d headers=%s total=%s", i+1, resp.StatusCode, resp.Proto, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"), resp.Header.Get("Cache-Control"), resp.Header.Get("ETag"), resp.Header.Get("Vary"), size, first, time.Since(start)))
			etag = resp.Header.Get("ETag")
		}
	}
	if r.state.ProxyRuntime == "docker" {
		proxy := r.state.Params.Proxy
		data, e := r.proxyCompose(ctx, proxy, "ps", "--all")
		rows = append(rows, r.safeOutput(data))
		if e != nil {
			return strings.Join(rows, "\n"), e
		}
		data, e = r.proxyCompose(ctx, proxy, "logs", "--tail", "40", "--no-color")
		rows = append(rows, r.safeOutput(data))
		if e != nil {
			return strings.Join(rows, "\n"), e
		}
		if proxy == "caddy" {
			// The admin API is loopback-only inside the container; this inspects the
			// active config rather than merely reading the Caddyfile on disk.
			data, e = r.proxyCompose(ctx, proxy, "exec", "-T", "caddy", "wget", "-qO-", "http://127.0.0.1:2019/config/")
			rows = append(rows, "Active Caddy config:\n"+r.safeOutput(data))
			if e != nil {
				return strings.Join(rows, "\n"), e
			}
		}
	}
	status, err := r.subscriptionStatus(ctx)
	rows = append(rows, "Subscription: "+status)
	rows = append(rows, "Browser verification required: Network (Disable cache OFF), repeated editor opens, WASM download vs compilation/initialization. No browser diagnosis was inferred from this report.")
	return strings.Join(rows, "\n"), err
}
