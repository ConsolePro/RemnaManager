package installer

import (
	"context"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

//go:embed setup.sh
var setupScript []byte

type Real struct {
	Root, Stack, LogPath, APIBase string
	SystemRoot                    string
	Runner                        Runner
	Client                        *http.Client
	mu                            sync.Mutex
	state                         State
	sshPorts                      []int
	lock, log                     *os.File
	controls                      map[string]services.NodeSSH
}

func NewReal() *Real {
	return &Real{Root: "/var/lib/remnamanager", Stack: "/opt/remnawave", APIBase: "http://127.0.0.1:3000/api", Runner: ExecRunner{}, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (r *Real) Defaults() services.PanelParameters {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.state.Params
	if p.Proxy == "" {
		p.Proxy = "nginx"
	}
	if p.Admin == "" {
		p.Admin = "admin"
	}
	if len(p.SSHPorts) == 0 {
		p.SSHPorts = append([]int(nil), r.sshPorts...)
	}
	p.SSHPorts = append([]int(nil), p.SSHPorts...)
	return p
}

// Installed reports the persisted installation result, not current service health.
func (r *Real) Installed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Ready && !r.state.FirewallPending && !r.state.ProtectionPending && !r.state.RemovalPending
}

func (r *Real) systemPath(path string) string {
	if r.SystemRoot == "" {
		return path
	}
	return filepath.Join(r.SystemRoot, strings.TrimPrefix(path, "/"))
}
func (r *Real) Check(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return "", errors.New("real installation requires Linux and root; use --demo for previews")
	}
	release, e := os.ReadFile("/etc/os-release")
	if e != nil {
		return "", e
	}
	supported := false
	for _, pair := range [][2]string{{"debian", "12"}, {"debian", "13"}, {"ubuntu", "22.04"}, {"ubuntu", "24.04"}} {
		if releaseValue(string(release), "ID") == pair[0] && releaseValue(string(release), "VERSION_ID") == pair[1] {
			supported = true
		}
	}
	if !supported {
		return "", errors.New("supported systems: Debian 12/13, Ubuntu 22.04/24.04")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", errors.New("amd64 or arm64 required")
	}
	if _, e = os.Stat("/run/systemd/system"); e != nil {
		return "", errors.New("a running systemd host is required")
	}
	s, e := loadState(statePath(r.Root))
	if e != nil {
		return "", e
	}
	r.state = s
	if s.RemovalPending {
		return "", errors.New("finish interrupted removal using Panel and components → Remove panel")
	}
	if s.ProtectionPending {
		return "", errors.New("resume the pending configuration using Panel and components → Panel protection")
	}
	if s.Ready && !s.FirewallPending {
		return "Installation already completed; saved credentials are available. Use component status to check running services.", nil
	}
	ports, e := DetectSSHPorts(ctx, r.Runner)
	if e != nil {
		return "", e
	}
	r.sshPorts = ports
	return fmt.Sprintf("%s/%s · SSH TCP %v · HTTPS 80/443\nDocker/Compose, panel, database, subscription and firewall will be configured.", runtime.GOOS, runtime.GOARCH, ports), nil
}
func releaseValue(s, key string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimPrefix(line, key+"="), "\"\r ")
		}
	}
	return ""
}

func (r *Real) Stages() [][2]string {
	return [][2]string{
		{"Проверка параметров, DNS и SSH", "Parameters, DNS and SSH checks"},
		{"Установка зависимостей и Docker", "Dependencies and Docker"},
		{"Создание закрытой конфигурации", "Private configuration"},
		{"Запуск PostgreSQL, Valkey и панели", "PostgreSQL, Valkey and panel startup"},
		{"Создание администратора и токенов", "Administrator and API tokens"},
		{"Запуск страницы подписки", "Subscription page startup"},
		{"Прокси и сертификаты HTTPS", "Reverse proxy and HTTPS certificates"},
		{"Проверка публичного HTTPS", "Public HTTPS checks"},
		{"Firewall с автоматическим откатом", "Firewall with automatic rollback"},
	}
}

func (r *Real) Step(ctx context.Context, stage int, p services.PanelParameters) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if stage < 0 || stage >= len(r.Stages()) {
		return errors.New("invalid installation stage")
	}
	if stage == 0 {
		services.ReportProgress(ctx, "Проверяю параметры установки", "Validating installation parameters")
	}
	if err := ValidateParameters(p); err != nil {
		return err
	}
	if stage == 0 {
		if runtime.GOOS != "linux" || os.Geteuid() != 0 {
			return errors.New("Linux root required")
		}
		if err := r.preflight(ctx, p); err != nil {
			return err
		}
		if err := os.MkdirAll(r.Root, 0700); err != nil {
			return err
		}
		if r.lock == nil {
			f, e := lockFile(filepath.Join(r.Root, "install.lock"))
			if e != nil {
				return errors.New("another installer is running")
			}
			r.lock = f
		}
		s, e := loadState(statePath(r.Root))
		if e != nil {
			return e
		}
		if s.RemovalPending {
			return errors.New("finish interrupted panel removal before installing again")
		}
		if s.RecoveryBackup != "" {
			return fmt.Errorf("backend recovery required before resuming installation; backup: %s", s.RecoveryBackup)
		}
		if s.SubRecovery != "" {
			return errors.New("subscription recovery required; run Subscription page → Install / start first")
		}
		if s.ProtectionPending {
			return errors.New("resume Panel protection before running the installer")
		}
		if s.Params.PanelDomain == "" {
			nodeSSH := s.NodeSSH
			s, e = newState(p)
			if e != nil {
				return e
			}
			s.NodeSSH = nodeSSH
			s.BackendTag = "3.4.9"
		} else {
			comparison := s.Params
			comparison.Email = p.Email
			comparison.SSHPorts = p.SSHPorts
			if s.Params.Protection == "" {
				comparison.Protection, comparison.AuthDomain = p.Protection, p.AuthDomain
				s.Completed[2], s.Completed[6] = false, false
			}
			if !reflect.DeepEqual(comparison, p) {
				return errors.New("resume with the saved domains, proxy and administrator; migration is not an overwrite")
			}
			if s.Params.Email != p.Email {
				s.Completed[6] = false
			}
			if !reflect.DeepEqual(s.Params.SSHPorts, p.SSHPorts) {
				s.Ready = false
			}
			s.Params = p
		}
		r.state = s
		if err := ensureProtection(&r.state); err != nil {
			return err
		}
		if r.log == nil {
			f, e := os.CreateTemp(r.Root, "install-*.log")
			if e != nil {
				return e
			}
			r.log = f
			r.LogPath = f.Name()
			if _, ok := r.Runner.(ExecRunner); ok {
				r.Runner = ExecRunner{Log: f}
			}
		}
	} else if r.state.Params.PanelDomain == "" {
		return errors.New("run preflight first")
	}
	if !reflect.DeepEqual(r.state.Params, p) {
		return errors.New("installation parameters changed during execution")
	}
	// Recheck readiness and firewall even on resume; no regenerated credentials.
	if r.state.Completed[stage] && (stage == 2 || stage == 6) {
		services.ReportProgress(ctx, "Настройки уже сохранены — использую готовую конфигурацию", "Configuration already saved — reusing existing settings")
		return nil
	}
	var err error
	switch stage {
	case 0:
		services.ReportProgress(ctx, "Сохраняю параметры и защищённое состояние установки", "Saving parameters and private installation state")
		err = saveState(statePath(r.Root), r.state)
	case 1:
		services.ReportProgress(ctx, "Проверяю установленные пакеты и зависимости", "Checking installed packages and dependencies")
		_, err = r.Runner.Run(ctx, setupScript, "bash", "-s", "--", p.Proxy, runtime.GOARCH)
	case 2:
		services.ReportProgress(ctx, "Проверяю совместимость firewall", "Checking firewall compatibility")
		if err = r.checkFirewall(ctx); err == nil {
			services.ReportProgress(ctx, "Создаю Compose-конфигурацию и закрытые файлы с секретами", "Creating Compose configuration and private secret files")
			err = r.configure()
		}
	case 3:
		services.ReportProgress(ctx, "Загружаю образы PostgreSQL, Valkey и панели", "Downloading PostgreSQL, Valkey and panel images")
		if _, err = r.compose(ctx, "pull", "remnawave", "remnawave-db", "remnawave-redis"); err == nil {
			services.ReportProgress(ctx, "Запускаю контейнеры и ожидаю готовности сервисов", "Starting containers and waiting for service health")
			_, err = r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave")
		}
	case 4:
		err = r.bootstrap(ctx)
	case 5:
		services.ReportProgress(ctx, "Сохраняю настройки и загружаю образ страницы подписки", "Saving settings and downloading the subscription page image")
		if err = writeFile(filepath.Join(r.Stack, "subscription.env"), renderSubEnv(r.state), 0600); err == nil {
			_, err = r.compose(ctx, "--profile", "subscription", "pull", "subscription")
		}
		if err == nil {
			var digest []byte
			services.ReportProgress(ctx, "Проверяю и фиксирую digest образа подписки", "Verifying and pinning the subscription image digest")
			digest, err = r.Runner.Run(ctx, nil, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", r.state.SubImage)
			if err == nil {
				image := strings.TrimSpace(string(digest))
				image = strings.TrimPrefix(image, "docker.io/")
				digestHex := strings.TrimPrefix(image, "remnawave/subscription-page@sha256:")
				decoded, decodeErr := hex.DecodeString(digestHex)
				if !strings.HasPrefix(image, "remnawave/subscription-page@sha256:") || decodeErr != nil || len(decoded) != 32 {
					err = errors.New("subscription image digest is missing")
				} else {
					r.state.SubImage = image
					err = saveState(statePath(r.Root), r.state)
					if err == nil {
						err = r.configure()
					}
				}
			}
		}
		if err == nil {
			services.ReportProgress(ctx, "Запускаю контейнер страницы подписки", "Starting the subscription page container")
			_, err = r.compose(ctx, "--profile", "subscription", "up", "-d", "subscription")
		}
		if err == nil {
			services.ReportProgress(ctx, "Ожидаю готовности страницы подписки", "Waiting for the subscription page to become ready")
			err = r.waitSubscription(ctx)
		}
	case 6:
		services.ReportProgress(ctx, "Настраиваю выбранную защиту панели", "Configuring the selected panel protection")
		if err = r.startProtection(ctx); err == nil {
			err = r.configureProxy(ctx)
		}
	case 7:
		err = r.waitHTTPS(ctx)
	case 8:
		services.ReportProgress(ctx, "Настраиваю firewall и автоматический откат; сохраняю доступ по SSH", "Configuring firewall and automatic rollback; preserving SSH access")
		err = r.applyFirewall(ctx)
	}
	if err != nil {
		_ = saveState(statePath(r.Root), r.state)
		return fmt.Errorf("%s: %w; private log: %s", r.Stages()[stage][1], err, r.LogPath)
	}
	r.state.Completed[stage] = true
	return saveState(statePath(r.Root), r.state)
}
func (r *Real) configure() error {
	if e := os.MkdirAll(r.Stack, 0700); e != nil {
		return e
	}
	data, e := RenderStack(r.state)
	if e != nil {
		return e
	}
	for path, data := range map[string][]byte{"compose.json": data, ".env": renderEnv(r.state), "subscription.env": renderSubEnv(r.state), ".remnamanager-go": []byte("managed\n")} {
		if e = writeFile(filepath.Join(r.Stack, path), data, 0600); e != nil {
			return e
		}
	}
	if protectionMode(r.state.Params) == "tinyauth" {
		return writeFile(tinyEnvPath(r.Stack), renderTinyEnv(r.state), 0600)
	}
	if protectionMode(r.state.Params) == "mfa" {
		if runtime.GOARCH == "arm64" {
			if err := writeFile(filepath.Join(r.Stack, "mfa-build", "Dockerfile"), []byte(mfaDockerfile), 0600); err != nil {
				return err
			}
		}
		if err := writeFile(filepath.Join(r.Stack, "mfa.env"), renderMFAEnv(r.state), 0600); err != nil {
			return err
		}
		return writeFile(filepath.Join(r.Stack, "mfa.Caddyfile"), []byte(RenderMFAGateway(r.state)), 0600)
	}
	return nil
}
func (r *Real) compose(ctx context.Context, args ...string) ([]byte, error) {
	all := []string{"compose", "--project-directory", r.Stack, "-f", filepath.Join(r.Stack, "compose.json")}
	all = append(all, args...)
	return r.Runner.Run(ctx, nil, "docker", all...)
}
func (r *Real) Summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	return accessDetails(s, r.Root, false) + "\nLog: " + r.LogPath
}
func (r *Real) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for path, settings := range r.controls {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = r.Runner.Run(ctx, nil, "ssh", "-F", "/dev/null", "-S", path, "-O", "exit", "root@"+settings.Host)
		cancel()
	}
	r.controls = nil
	if r.log != nil {
		_ = r.log.Close()
		r.log = nil
	}
	if r.lock != nil {
		e := r.lock.Close()
		r.lock = nil
		return e
	}
	return nil
}

func (r *Real) preflight(ctx context.Context, p services.PanelParameters) error {
	services.ReportProgress(ctx, "Проверяю сервер и отсутствие чужой установки", "Checking the server and existing installations")
	if _, e := os.Stat(filepath.Join(r.Stack, ".remnamanager-go")); errors.Is(e, os.ErrNotExist) {
		if entries, e := os.ReadDir(r.Stack); e == nil && len(entries) > 0 {
			return errors.New("existing /opt/remnawave is not managed by this installer; refusing overwrite")
		}
	}
	for _, domain := range proxyDomains(p) {
		services.ReportProgress(ctx, "Проверяю DNS домена "+domain, "Checking DNS for "+domain)
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, domain)
		if e != nil || len(ips) == 0 {
			return fmt.Errorf("DNS is not ready for %s", domain)
		}
	}
	services.ReportProgress(ctx, "Определяю SSH-порты и проверяю сохранение доступа", "Detecting SSH ports and verifying access preservation")
	ssh, e := DetectSSHPorts(ctx, r.Runner)
	if e != nil {
		return e
	}
	for _, port := range ssh {
		found := false
		for _, keep := range p.SSHPorts {
			if keep == port {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("SSH port %d must be preserved", port)
		}
	}
	services.ReportProgress(ctx, "Проверяю доступность портов панели и прокси", "Checking panel and proxy port availability")
	data, e := r.Runner.Run(ctx, nil, "ss", "-H", "-ltnp")
	if e != nil {
		return e
	}
	managed := r.state.Params.PanelDomain != ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		_, port, _ := net.SplitHostPort(fields[3])
		n, _ := strconv.Atoi(port)
		if n != 80 && n != 443 && n != 3000 && n != 3010 && (n != 3020 || protectionMode(p) != "tinyauth") && (n != 3030 || protectionMode(p) != "mfa") {
			continue
		}
		if !managed {
			return fmt.Errorf("port %d is already in use; no existing service will be stopped", n)
		}
	}
	if !managed && p.Proxy == "caddy" {
		if _, e = os.Stat(r.systemPath("/etc/caddy/Caddyfile")); e == nil {
			return errors.New("existing Caddyfile is not managed; refusing overwrite")
		}
	}
	if !managed && p.Proxy == "nginx" {
		if _, e = os.Stat(r.systemPath("/etc/nginx")); e == nil {
			retained, _ := os.ReadFile(filepath.Join(r.Root, "retained-proxy"))
			if string(retained) != "nginx\n" {
				return errors.New("existing Nginx configuration requires explicit migration; refusing overwrite")
			}
		}
	}
	return nil
}
