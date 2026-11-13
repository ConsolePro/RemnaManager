package installer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/services"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

type State struct {
	DetachedSubscription                         *services.PanelParameters
	NodeSSH                                      map[string]services.NodeSSH
	Params                                       services.PanelParameters
	BackendTag, SubImage                         string
	SubSource                                    string
	LayoutVersion                                int
	ProxyRecovery, ProxyTarget, ProtectionBackup string
	// Empty on installations created before Docker proxy support.
	ProxyRuntime                                                         string
	DBPassword, AppSecret, MetricsPassword, WebhookSecret, AdminPassword string
	ManagerToken, SubToken                                               string
	CookieKey, CookieValue, TinyPassword, TinyHash                       string
	MFAPassword, MFASigningKey                                           string
	ProtectionPending                                                    bool
	RemovalPending                                                       bool
	RemovalPhase                                                         int
	Removed                                                              bool
	Completed                                                            map[int]bool
	FirewallPending                                                      bool
	FirewallOwned                                                        bool
	RollbackAt                                                           time.Time
	Ready                                                                bool
	// An interrupted backend update may already have migrated the database.
	RecoveryBackup  string
	SubRecovery     string
	RemoteLastError string
}

func secret() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func newState(p services.PanelParameters) (State, error) {
	s := State{Params: p, Completed: map[int]bool{}, SubImage: "remnawave/subscription-page:latest"}
	for _, target := range []*string{&s.DBPassword, &s.AppSecret, &s.MetricsPassword, &s.WebhookSecret, &s.AdminPassword} {
		value, e := secret()
		if e != nil {
			return s, e
		}
		*target = value
	}
	s.AdminPassword = "Rm9" + s.AdminPassword[:40]
	if e := ensureProtection(&s); e != nil {
		return s, e
	}
	return s, nil
}
func loadState(path string) (State, error) {
	var s State
	data, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	e = decodeState(path, data, &s)
	removed := s.Removed && s.Params.PanelDomain == "" && !s.RemovalPending
	if e == nil && !removed && (s.Completed == nil || s.DBPassword == "" || s.AdminPassword == "") {
		e = errors.New("invalid installation state; restore backup rather than overwriting secrets")
	}
	return s, e
}
func saveState(path string, s State) error {
	data, e := encodeState(path, s)
	if e != nil {
		return e
	}
	return writeFile(path, append(data, '\n'), 0600)
}

func ValidateParameters(p services.PanelParameters) error {
	if panelCA(p) != "letsencrypt" && panelCA(p) != "zerossl" {
		return errors.New("certificate authority must be letsencrypt or zerossl")
	}
	if p.RemoteSubscription != nil {
		if err := ValidateSubscriptionRemote(*p.RemoteSubscription); err != nil {
			return err
		}
	}
	if !ValidDomain(p.PanelDomain) || !ValidDomain(p.SubscriptionDomain) || strings.EqualFold(p.PanelDomain, p.SubscriptionDomain) {
		return errors.New("two different valid domains are required")
	}
	if net.ParseIP(p.PanelDomain) != nil || net.ParseIP(p.SubscriptionDomain) != nil {
		return errors.New("public TLS requires DNS names, not IP addresses")
	}
	if p.Proxy != "nginx" && p.Proxy != "caddy" {
		return errors.New("proxy must be nginx or caddy")
	}
	if err := ValidateProtection(p); err != nil {
		return err
	}
	if !config.ValidEmail(p.Email) {
		return errors.New("valid ACME email is required")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`).MatchString(p.Admin) {
		return errors.New("admin name: 3–30 letters, digits or underscore")
	}
	if len(p.SSHPorts) == 0 {
		return errors.New("at least one SSH port must be preserved")
	}
	for _, port := range p.SSHPorts {
		if port < 1 || port > 65535 || port == 80 || port == 443 || port == 3000 || port == 3010 || (port == 3020 && protectionMode(p) == "tinyauth") || (port == 3030 && protectionMode(p) == "mfa") {
			return errors.New("invalid or conflicting SSH port")
		}
	}
	return nil
}
func ParsePorts(text string) ([]int, error) {
	seen := map[int]bool{}
	ports := []int{}
	for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, e := strconv.Atoi(field)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("SSH ports must be numbers from 1 to 65535")
		}
		if !seen[n] {
			ports = append(ports, n)
			seen[n] = true
		}
	}
	if len(ports) == 0 {
		return nil, errors.New("SSH port is required")
	}
	sort.Ints(ports)
	return ports, nil
}
func statePath(root string) string { return filepath.Join(root, "panel.json") }
