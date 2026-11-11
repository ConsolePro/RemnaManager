package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// All deletion is limited to known files and the installer's fixed Docker project.
// Keep state until cleanup completes so interrupted removal can be resumed.
func (r *Real) removePanel(ctx context.Context) error {
	for _, directory := range []string{r.Root, r.Stack} {
		if err := noSymlinkParents(directory); err != nil {
			return err
		}
	}
	if r.state.RemovalPhase < 1 {
		if err := r.validateRemovalProxy(); err != nil {
			return err
		}
		r.state.RemovalPending = true
		if err := saveState(statePath(r.Root), r.state); err != nil {
			return err
		}
		if err := r.removePanelProxy(ctx); err != nil {
			return err
		}
		if err := r.removePanelFirewall(ctx); err != nil {
			return err
		}
		r.state.RemovalPhase = 1
		if err := saveState(statePath(r.Root), r.state); err != nil {
			return err
		}
	}
	if r.state.RemovalPhase < 2 {
		// A trusted manifest also removes dormant TinyAuth/MFA volumes after a
		// protection switch. It needs no env files from an incomplete installation.
		services := map[string]any{}
		for _, name := range []string{"remnawave", "remnawave-db", "remnawave-redis", "subscription", "tinyauth", "mfa-gateway"} {
			services[name] = map[string]any{"image": "scratch", "networks": []string{"panel"}}
		}
		volumes := map[string]any{}
		mounts := []string{}
		for _, name := range []string{"database", "valkey-socket", "tinyauth-data", "mfa-data"} {
			volumes[name] = map[string]any{}
			mounts = append(mounts, name+":/remove/"+name)
		}
		// Compose prunes unused volume definitions before down. Reference every
		// owned volume so data from previously selected protection modes is removed.
		services["remnawave"].(map[string]any)["volumes"] = mounts
		data, err := json.Marshal(map[string]any{"name": "remnamanager-panel", "services": services, "volumes": volumes, "networks": map[string]any{"panel": map[string]any{}}})
		if err != nil {
			return err
		}
		manifest, err := os.CreateTemp(r.Root, "remove-*.json")
		if err != nil {
			return err
		}
		path := manifest.Name()
		defer os.Remove(path)
		_, err = manifest.Write(data)
		closeErr := manifest.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		_, err = r.Runner.Run(ctx, nil, "docker", "compose", "--project-directory", r.Stack, "-f", path, "down", "--volumes", "--remove-orphans")
		if err != nil && !(errors.Is(err, exec.ErrNotFound) && !r.state.Completed[2]) {
			return err
		}
		r.state.RemovalPhase = 2
		if err := saveState(statePath(r.Root), r.state); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, name := range []string{"compose.json", ".env", "subscription.env", "tinyauth.env", "mfa.env", "mfa.Caddyfile", "mfa-build/Dockerfile"} {
		if err := removeKnownFile(filepath.Join(r.Stack, name)); err != nil {
			return err
		}
	}
	// Remove an empty build directory only; never recurse into user content.
	_ = os.Remove(filepath.Join(r.Stack, "mfa-build"))
	if err := removeKnownFile(filepath.Join(r.Stack, ".remnamanager-go")); err != nil {
		return err
	}
	// Keep the Nginx ownership record across later Caddy install/remove cycles.
	if r.state.Params.Proxy == "nginx" {
		if err := writeFile(filepath.Join(r.Root, "retained-proxy"), []byte("nginx\n"), 0600); err != nil {
			return err
		}
	}
	for _, name := range []string{"firewall-apply.sh", "firewall-active.nft", "firewall-rollback.nft", "nginx.previous", "caddy.previous"} {
		if err := removeKnownFile(filepath.Join(r.Root, name)); err != nil {
			return err
		}
	}
	clean := State{NodeSSH: r.state.NodeSSH, Removed: true}
	if err := saveState(statePath(r.Root), clean); err != nil {
		return err
	}
	r.state = clean
	return nil
}

func (r *Real) removablePartialStack(s State) bool {
	if s.Completed[2] {
		return false
	}
	if err := noSymlinkParents(r.Stack); err != nil {
		return false
	}
	entries, err := os.ReadDir(r.Stack)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "compose.json", ".env", "subscription.env", "tinyauth.env", "mfa.env", "mfa.Caddyfile":
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func noSymlinkParents(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing removal through symlink: %s", current)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func removeKnownFile(path string) error {
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular file: %s", path)
	}
	return os.Remove(path)
}

func (r *Real) removalProxyPath() string {
	if r.state.Params.Proxy == "caddy" {
		return r.systemPath("/etc/caddy/Caddyfile")
	}
	return r.systemPath("/etc/nginx/conf.d/remnamanager-go.conf")
}

func (r *Real) validateRemovalProxy() error {
	path := r.removalProxyPath()
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	valid := string(data) == RenderCaddy(r.state)
	if r.state.Params.Proxy == "nginx" {
		valid = string(data) == RenderNginx(r.state, true) || string(data) == RenderNginx(r.state, false)
	}
	if !valid {
		return fmt.Errorf("proxy configuration has external changes; refusing removal: %s", path)
	}
	return nil
}

func (r *Real) removePanelProxy(ctx context.Context) error {
	if err := r.validateRemovalProxy(); err != nil {
		return err
	}
	path := r.removalProxyPath()
	_, fileErr := os.Stat(path)
	if r.state.Params.Proxy == "caddy" && (fileErr == nil || r.state.Completed[1]) {
		if _, err := r.Runner.Run(ctx, nil, "systemctl", "disable", "--now", "caddy"); err != nil {
			return err
		}
	}
	if err := removeKnownFile(path); err != nil {
		return err
	}
	if r.state.Params.Proxy == "nginx" && (fileErr == nil || r.state.Completed[1]) {
		if _, err := r.Runner.Run(ctx, nil, "systemctl", "is-active", "--quiet", "nginx"); err == nil {
			if _, err := r.Runner.Run(ctx, nil, "nginx", "-t"); err != nil {
				return err
			}
			if _, err := r.Runner.Run(ctx, nil, "systemctl", "reload", "nginx"); err != nil {
				return err
			}
		}
	}
	hook := r.systemPath("/etc/letsencrypt/renewal-hooks/deploy/remnamanager-go-nginx")
	data, err := os.ReadFile(hook)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if string(data) != "#!/bin/sh\nset -eu\n/usr/sbin/nginx -t\n/bin/systemctl reload nginx\n" {
			return errors.New("certificate renewal hook has external changes; refusing removal")
		}
		return removeKnownFile(hook)
	}
	return nil
}

func (r *Real) removePanelFirewall(ctx context.Context) error {
	if !r.state.FirewallOwned && !r.state.FirewallPending {
		return nil
	}
	for _, unit := range []string{rollbackUnit + ".timer", rollbackUnit + ".service"} {
		if _, err := r.Runner.Run(ctx, nil, "systemctl", "is-active", "--quiet", unit); err == nil {
			if _, err := r.Runner.Run(ctx, nil, "systemctl", "stop", unit); err != nil {
				return err
			}
		}
	}
	path := r.systemPath("/etc/systemd/system/remnamanager-firewall.service")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		expected := fmt.Sprintf("[Unit]\nDescription=RemnaManager firewall\nAfter=network-pre.target\nBefore=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s/firewall-apply.sh\nExecReload=%s/firewall-apply.sh\n[Install]\nWantedBy=multi-user.target\n", r.Root, r.Root)
		if string(data) != expected {
			return errors.New("firewall service has external changes; refusing removal")
		}
		if _, err := r.Runner.Run(ctx, nil, "systemctl", "disable", "--now", "remnamanager-firewall.service"); err != nil {
			return err
		}
		if err := removeKnownFile(path); err != nil {
			return err
		}
		if _, err := r.Runner.Run(ctx, nil, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	tables, err := r.Runner.Run(ctx, nil, "nft", "list", "tables")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(tables), "\n") {
		if strings.TrimSpace(line) == "table inet "+firewallTable {
			_, err = r.Runner.Run(ctx, nil, "nft", "delete", "table", "inet", firewallTable)
			return err
		}
	}
	return nil
}
