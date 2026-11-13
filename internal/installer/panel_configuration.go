package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// This transaction changes domains or certificates only. Placement is never
// inferred from domain input and cannot migrate between one and two servers.
func (r *Real) changePanelConfiguration(ctx context.Context, request services.PanelRequest) error {
	if r.state.LayoutVersion < officialLayout {
		return errors.New("domain and certificate edits require the official Compose layout; legacy installations need explicit migration")
	}
	if r.state.ConfigurationBackup != "" {
		return r.restorePanelConfiguration(ctx, r.state.ConfigurationBackup)
	}
	old := r.state
	p := old.Params
	if request.Action == "panel-domains" {
		names := strings.Fields(strings.ToLower(request.Value))
		if len(names) != 2 {
			return errors.New("provide panel and subscription domains")
		}
		p.PanelDomain, p.SubscriptionDomain = names[0], names[1]
		if protectionMode(p) == "tinyauth" {
			p.AuthDomain = "auth." + p.PanelDomain
		}
	} else {
		p.Certificate = request.Certificate
	}
	if err := ValidateParameters(p); err != nil {
		return err
	}
	if err := r.checkCertificateParameters(ctx, p); err != nil {
		return err
	}
	if err := r.preflight(ctx, p); err != nil {
		return err
	}
	backup, err := r.backup(ctx, "pre-panel-configuration")
	if err != nil {
		return err
	}

	r.state.Params = p
	r.state.ConfigurationBackup = backup
	if err = saveState(statePath(r.Root), r.state); err != nil {
		r.state = old
		return err
	}
	apply := func() error {
		if p.Proxy == "nginx" && certificateMethod(p) == "custom" {
			if err := r.disablePanelACME(ctx, old.Params.PanelDomain); err != nil {
				return err
			}
		}
		envPath := filepath.Join(r.Stack, ".env")
		data, e := os.ReadFile(envPath)
		if e != nil {
			return e
		}
		data = patchDomainEnv(data, p)
		if e = writeFile(envPath, data, 0600); e != nil {
			return e
		}
		if e = r.configure(); e != nil {
			return e
		}
		if _, e = r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave"); e != nil {
			return e
		}
		if e = r.startProtection(ctx); e != nil {
			return e
		}
		if request.Action == "panel-certificates" && p.Proxy == "nginx" {
			if e = r.installPanelCertificates(ctx); e != nil {
				return e
			}
		}
		if e = r.configureProxy(ctx); e != nil {
			return e
		}
		if p.RemoteSubscription != nil {
			if _, e = r.remoteSubscription(ctx, "install", ""); e != nil {
				return e
			}
		}
		return r.waitHTTPS(ctx)
	}
	if err = apply(); err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		restoreErr := r.restorePanelConfiguration(recovery, backup)
		return errors.Join(fmt.Errorf("configuration failed; recovery backup: %s: %w", backup, err), restoreErr)
	}
	r.state.ConfigurationBackup = ""
	return saveState(statePath(r.Root), r.state)
}

func patchDomainEnv(data []byte, p services.PanelParameters) []byte {
	values := map[string]string{"PANEL_DOMAIN": p.PanelDomain, "FRONT_END_DOMAIN": p.PanelDomain, "SUB_PUBLIC_DOMAIN": p.SubscriptionDomain}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if value, found := values[key]; ok && found {
			lines[i] = key + "=" + value
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
func snapshotConfigurationFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(source, target)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeFile(target, data, 0600)
}
func (r *Real) restorePanelConfiguration(ctx context.Context, backup string) error {
	if filepath.Dir(backup) != filepath.Clean(r.Root) {
		return errors.New("invalid configuration backup path")
	}
	if _, err := r.verifyBackup(filepath.Base(backup)); err != nil {
		return err
	}
	saved, err := loadState(statePath(backup))
	if err != nil {
		return err
	}
	r.state = saved
	r.state.ConfigurationBackup = backup
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return err
	}

	// Move the failed renewal configuration aside, including files that did not
	// exist before this transaction. A failed DNS/custom switch cannot replace
	// the restored certificate later through a newly scheduled cron job.
	for _, name := range []string{"acme", "dns-token.key", "dns-token.enc", "renew-dns.sh", "reload-nginx.sh", "acme-relay.sh"} {
		source := filepath.Join(r.Root, name)
		if _, e := os.Stat(source); e == nil {
			if e = os.Rename(source, source+".failed-"+fmt.Sprint(time.Now().UnixNano())); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if _, e := os.Stat(filepath.Join(backup, "renewal")); e == nil {
		if err = copyTree(filepath.Join(backup, "renewal"), r.Root); err != nil {
			return err
		}
	}
	if _, e := os.Stat(filepath.Join(r.Root, "acme", "acme.sh")); e == nil {
		if err = r.ensureACMERenewal(ctx); err != nil {
			return err
		}
	} else if os.IsNotExist(e) {
		script := `set -Eeuo pipefail
manager=$1
entries=$(crontab -l 2>/dev/null || true)
kept=""
while IFS= read -r line; do
 normalized=${line//\"/}; normalized=${normalized//\'/}
 if [[ $normalized == *" $manager/acme/acme.sh --cron "* || $line == *"# RemnaManager DNS $manager" ]]; then continue; fi
 kept+="$line"$'\n'
done <<< "$entries"
printf '%s' "$kept" | crontab -
`
		if _, err = r.Runner.Run(ctx, []byte(script), "bash", "-s", "--", r.Root); err != nil {
			return err
		}
	} else {
		return e
	}

	for _, name := range []string{".env", "tinyauth.env", "mfa.env", "mfa.Caddyfile"} {
		data, e := os.ReadFile(filepath.Join(backup, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if e = writeFile(filepath.Join(r.Stack, name), data, 0600); e != nil {
			return e
		}
	}
	if err = copyTree(filepath.Join(backup, "proxy"), r.Stack); err != nil {
		return err
	}
	if err = r.configure(); err != nil {
		return err
	}
	if _, err = r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave"); err != nil {
		return err
	}
	if err = r.startProtection(ctx); err != nil {
		return err
	}
	if err = r.validateDockerProxy(ctx); err != nil {
		return err
	}
	if _, err = r.proxyCompose(ctx, r.state.Params.Proxy, "up", "-d", "--force-recreate"); err != nil {
		return err
	}
	if r.state.Params.RemoteSubscription != nil {
		if _, err = r.remoteSubscription(ctx, "install", ""); err != nil {
			return err
		}
	}
	if err = r.waitHTTPS(ctx); err != nil {
		return err
	}
	r.state.ConfigurationBackup = ""
	return saveState(statePath(r.Root), r.state)
}
