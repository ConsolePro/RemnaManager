package installer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"golang.org/x/crypto/bcrypt"
)

const tinyAuthImage = "ghcr.io/tinyauthapp/tinyauth:v5.2.0"

func protectionMode(p services.PanelParameters) string {
	if p.Protection == "" {
		return "cookie"
	}
	return p.Protection
}

func ValidateProtection(p services.PanelParameters) error {
	switch protectionMode(p) {
	case "cookie":
		if p.AuthDomain != "" {
			return errors.New("Cookie protection does not use an authentication domain")
		}
	case "tinyauth":
		// A direct child makes TinyAuth's parent-domain session cookie valid on
		// the panel, without sharing it with the public subscription domain.
		prefix := strings.TrimSuffix(p.AuthDomain, "."+p.PanelDomain)
		if !ValidDomain(p.AuthDomain) || prefix == p.AuthDomain || strings.Contains(prefix, ".") || p.AuthDomain == p.SubscriptionDomain {
			return errors.New("TinyAuth domain must be a direct child of the panel domain, e.g. auth.panel.example.com, different from the subscription domain")
		}
	case "mfa":
		if p.AuthDomain != "" {
			return errors.New("MFA portal uses the panel domain at /r; no additional domain is required")
		}
	default:
		return errors.New("panel protection must be cookie, tinyauth or mfa")
	}
	return nil
}

func ensureProtection(s *State) error {
	if err := ValidateProtection(s.Params); err != nil {
		return err
	}
	if protectionMode(s.Params) == "cookie" {
		if s.CookieKey == "" {
			value, err := secret()
			if err != nil {
				return err
			}
			s.CookieKey = "rm_" + value
		}
		if s.CookieValue == "" {
			value, err := secret()
			if err != nil {
				return err
			}
			s.CookieValue = value
		}
	} else if protectionMode(s.Params) == "mfa" {
		if s.MFAPassword == "" {
			value, err := secret()
			if err != nil {
				return err
			}
			s.MFAPassword = "Mf9" + value[:40]
		}
		if s.MFASigningKey == "" {
			value, err := secret()
			if err != nil {
				return err
			}
			s.MFASigningKey = value
		}
	} else {
		if s.TinyPassword == "" {
			value, err := secret()
			if err != nil {
				return err
			}
			s.TinyPassword = "Ta9" + value[:40]
		}
		if s.TinyHash == "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(s.TinyPassword), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			s.TinyHash = string(hash)
		}
	}
	return validateProtectionState(*s)
}

func validateProtectionState(s State) error {
	if err := ValidateProtection(s.Params); err != nil {
		return err
	}
	if protectionMode(s.Params) == "cookie" {
		if !regexp.MustCompile(`^rm_[a-f0-9]{64}$`).MatchString(s.CookieKey) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(s.CookieValue) {
			return errors.New("saved Cookie protection secrets are missing or invalid; configure Panel protection first")
		}
	} else if protectionMode(s.Params) == "mfa" {
		if s.MFAPassword == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(s.MFASigningKey) {
			return errors.New("saved MFA bootstrap password or signing key is missing or invalid")
		}
	} else if s.TinyPassword == "" {
		return errors.New("saved TinyAuth password is missing")
	} else if _, err := bcrypt.Cost([]byte(s.TinyHash)); err != nil {
		return errors.New("saved TinyAuth password hash is invalid")
	}
	return nil
}

func panelAccessURL(s State) string {
	if protectionMode(s.Params) == "mfa" {
		return "https://" + s.Params.PanelDomain + "/r"
	}
	base := "https://" + s.Params.PanelDomain + "/auth/login"
	if protectionMode(s.Params) == "cookie" && s.CookieKey != "" && !s.ProtectionPending {
		return base + "?" + url.QueryEscape(s.CookieKey) + "=" + url.QueryEscape(s.CookieValue)
	}
	return base
}

func accessDetails(s State, root string, ru bool) string {
	text := fmt.Sprintf("Panel: %s\nAdmin: %s\nSaved initial password: %s\nSubscription: https://%s/<shortUuid>\nCredentials: %s (0600)", panelAccessURL(s), s.Params.Admin, s.AdminPassword, s.Params.SubscriptionDomain, statePath(root))
	if ru {
		text = fmt.Sprintf("Панель: %s\nЛогин: %s\nСохранённый первоначальный пароль: %s\nПодписка: https://%s/<shortUuid>\nФайл доступа: %s (0600)", panelAccessURL(s), s.Params.Admin, s.AdminPassword, s.Params.SubscriptionDomain, statePath(root))
	}
	if s.ProtectionPending {
		if ru {
			return text + "\nНастройка защиты не завершена. Повторите «Защита панели»."
		}
		return text + "\nProtection configuration is incomplete. Retry Panel protection."
	}
	if protectionMode(s.Params) == "tinyauth" {
		if ru {
			text += fmt.Sprintf("\nTinyAuth: https://%s\nЛогин TinyAuth: %s\nПервоначальный пароль TinyAuth: %s", s.Params.AuthDomain, s.Params.Admin, s.TinyPassword)
		} else {
			text += fmt.Sprintf("\nTinyAuth: https://%s\nTinyAuth login: %s\nTinyAuth initial password: %s", s.Params.AuthDomain, s.Params.Admin, s.TinyPassword)
		}
	} else if protectionMode(s.Params) == "mfa" {
		if ru {
			text += fmt.Sprintf("\nПортал MFA: https://%s/r\nЛогин портала: %s\nПервоначальный пароль портала: %s", s.Params.PanelDomain, s.Params.Admin, s.MFAPassword)
		} else {
			text += fmt.Sprintf("\nMFA portal: https://%s/r\nMFA login: %s\nMFA initial password: %s", s.Params.PanelDomain, s.Params.Admin, s.MFAPassword)
		}
		if ru {
			text += "\nПри первом входе настройте второй фактор. API-ключи: /r/settings/apikeys."
		} else {
			text += "\nEnroll a second factor on first login. API keys: /r/settings/apikeys."
		}
	} else if s.CookieKey == "" {
		if ru {
			text += "\nСтарая установка без Cookie-защиты. Выберите «Защита панели»."
		} else {
			text += "\nLegacy installation without Cookie protection. Choose Panel protection."
		}
	}
	return text
}

func renderTinyEnv(s State) []byte {
	// Single quotes in env_file preserve bcrypt's dollar signs literally.
	return []byte(fmt.Sprintf("TINYAUTH_APPURL=https://%s\nTINYAUTH_AUTH_USERS='%s:%s'\nTINYAUTH_AUTH_SECURECOOKIE=true\nTINYAUTH_AUTH_SUBDOMAINSENABLED=true\nTINYAUTH_DATABASE_PATH=/data/tinyauth.db\nTINYAUTH_LABELPROVIDER=none\nTINYAUTH_ANALYTICS_ENABLED=false\n", s.Params.AuthDomain, s.Params.Admin, s.TinyHash))
}

func (r *Real) startProtection(ctx context.Context) error {
	service := protectionService(r.state.Params)
	if service == "" {
		return nil
	}
	if err := validateProtectionState(r.state); err != nil {
		return err
	}
	if service == "mfa-gateway" && runtime.GOARCH == "arm64" {
		services.ReportProgress(ctx, "Проверяю образ MFA для ARM64", "Checking the ARM64 MFA image")
		buildDirectory := filepath.Join(r.Stack, "mfa-build")
		if err := writeFile(filepath.Join(buildDirectory, "Dockerfile"), []byte(mfaDockerfile), 0600); err != nil {
			return err
		}
		architecture, inspectErr := r.Runner.Run(ctx, nil, "docker", "image", "inspect", "--format", "{{.Architecture}}", mfaLocalImage)
		if inspectErr != nil || strings.TrimSpace(string(architecture)) != "arm64" {
			services.ReportProgress(ctx, "Собираю MFA-шлюз для ARM64; это может занять несколько минут", "Building the ARM64 MFA gateway; this may take several minutes")
			if _, err := r.Runner.Run(ctx, nil, "docker", "build", "--pull", "--platform", "linux/arm64", "--tag", mfaLocalImage, buildDirectory); err != nil {
				return err
			}
		}
	} else {
		services.ReportProgress(ctx, "Загружаю образ сервиса защиты "+service, "Downloading protection service image: "+service)
		if _, err := r.compose(ctx, "pull", service); err != nil {
			return err
		}
	}
	services.ReportProgress(ctx, "Запускаю сервис защиты и ожидаю готовности", "Starting the protection service and waiting for readiness")
	_, err := r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "120", service)
	return err
}

func (r *Real) panelServices() []string {
	names := []string{"remnawave", "subscription", "remnawave-db", "remnawave-redis"}
	if service := protectionService(r.state.Params); service != "" {
		names = append(names, service)
	}
	return names
}

func (r *Real) changeProtection(ctx context.Context, mode, domain string) error {
	p := r.state.Params
	p.Protection, p.AuthDomain = mode, strings.ToLower(strings.TrimSpace(domain))
	if err := ValidateParameters(p); err != nil {
		return err
	}
	if mode == "tinyauth" {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, p.AuthDomain)
		if err != nil || len(ips) == 0 {
			return fmt.Errorf("DNS is not ready for %s", p.AuthDomain)
		}
	}
	if r.state.ProtectionPending && (r.state.Params.Protection != mode || r.state.Params.AuthDomain != p.AuthDomain) {
		return errors.New("resume the pending protection mode and authentication domain first")
	}
	if oldService := protectionService(r.state.Params); oldService != "" && oldService != protectionService(p) {
		// Stop the old service while its Compose definition is still present.
		// Its data volume is retained; a later switch back reuses the sessions.
		if _, err := r.compose(ctx, "stop", oldService); err != nil {
			return err
		}
	}
	// Persist generated credentials before changing Compose or the proxy. On a
	// crash this journal makes retries reuse the same key and password.
	r.state.Params = p
	if err := ensureProtection(&r.state); err != nil {
		return err
	}
	r.state.ProtectionPending = true
	if err := saveState(statePath(r.Root), r.state); err != nil {
		return err
	}
	if err := r.configure(); err != nil {
		return err
	}
	if _, err := r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave"); err != nil {
		return err
	}
	if err := r.startProtection(ctx); err != nil {
		return err
	}
	if err := r.configureProxy(ctx); err != nil {
		return err
	}
	if err := r.checkPanelProtection(ctx); err != nil {
		return err
	}
	r.state.ProtectionPending = false
	r.state.Completed[6], r.state.Completed[7] = true, true
	return saveState(statePath(r.Root), r.state)
}

// Check both sides of the gate: a public panel must reject an unauthenticated
// request, while a Cookie-authenticated request must still reach the backend.
func (r *Real) checkPanelProtection(ctx context.Context) error {
	if err := validateProtectionState(r.state); err != nil {
		return err
	}
	client := *r.Client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	target := "https://" + r.state.Params.PanelDomain + "/api/auth/status"
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 Gecko/20100101 Firefox/140.0 (RemnaManager protection check)")
	resp, err := client.Do(request)
	if err != nil {
		return errors.New("cannot verify public panel protection")
	}
	resp.Body.Close()
	if protectionMode(r.state.Params) == "cookie" {
		if resp.StatusCode != http.StatusForbidden {
			return errors.New("panel must return HTTP 403 without the access cookie")
		}
		request.AddCookie(&http.Cookie{Name: r.state.CookieKey, Value: r.state.CookieValue})
		resp, err = client.Do(request)
		if err != nil {
			return errors.New("cannot reach panel with the access cookie")
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("panel with access cookie: HTTP %d", resp.StatusCode)
		}
	} else if protectionMode(r.state.Params) == "mfa" {
		location, parseErr := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || parseErr != nil || location.Scheme != "https" || location.Host != r.state.Params.PanelDomain || (location.Path != "/r" && !strings.HasPrefix(location.Path, "/r/")) {
			return errors.New("panel must redirect unauthenticated requests to the MFA portal")
		}
		request, _ = http.NewRequestWithContext(ctx, "GET", "https://"+r.state.Params.PanelDomain+"/r", nil)
		resp, err = client.Do(request)
		if err != nil {
			return errors.New("cannot reach MFA portal")
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return fmt.Errorf("MFA portal: HTTP %d", resp.StatusCode)
		}
	} else {
		location, parseErr := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || parseErr != nil || location.Scheme != "https" || location.Host != r.state.Params.AuthDomain {
			return errors.New("panel must redirect unauthenticated requests to the configured TinyAuth domain")
		}
		request, _ = http.NewRequestWithContext(ctx, "GET", "https://"+r.state.Params.AuthDomain+"/", nil)
		resp, err = client.Do(request)
		if err != nil {
			return errors.New("cannot reach public TinyAuth login")
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("TinyAuth login: HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

func tinyEnvPath(stack string) string { return filepath.Join(stack, "tinyauth.env") }

func protectionService(p services.PanelParameters) string {
	switch protectionMode(p) {
	case "tinyauth":
		return "tinyauth"
	case "mfa":
		return "mfa-gateway"
	}
	return ""
}
