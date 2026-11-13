package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type backupManifest struct {
	Version         int
	Created         time.Time
	Reason, Backend string
	Files           map[string]string
}

var backupNamePattern = regexp.MustCompile(`^backup-[0-9]{8}-[0-9]{6}-[0-9]+$`)

func fileHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("backup contains a non-regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyTree(source, target string) error {
	if err := noSymlinkParents(source); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("cannot snapshot special file %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFile(filepath.Join(target, rel), data, 0600)
	})
}

func (r *Real) completeBackup(ctx context.Context, directory string, reasons ...string) error {
	if r.state.LayoutVersion >= 1 && r.state.Params.RemoteSubscription == nil {
		data, err := os.ReadFile(r.subComposePath())
		if err != nil {
			return err
		}
		if err = writeFile(filepath.Join(directory, "subscription", "docker-compose.yml"), data, 0600); err != nil {
			return err
		}
	}
	reason := "manual"
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	if reason == "pre-panel-configuration" {
		for _, name := range []string{"acme", "dns-token.key", "dns-token.enc", "renew-dns.sh", "reload-nginx.sh", "acme-relay.sh"} {
			source := filepath.Join(r.Root, name)
			if _, e := os.Stat(source); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return e
			}
			if e := snapshotConfigurationFile(source, filepath.Join(directory, "renewal", name)); e != nil {
				return e
			}
		}
	}
	if r.state.ProxyRuntime == "docker" {
		for _, proxy := range []string{"nginx", "caddy"} {
			source := filepath.Join(r.Stack, proxy)
			if _, err := os.Stat(source); os.IsNotExist(err) {
				continue
			}
			if err := copyTree(source, filepath.Join(directory, "proxy", proxy)); err != nil {
				return err
			}
		}
	} else {
		path := r.removalProxyPath()
		data, err := os.ReadFile(path)
		if err == nil {
			err = writeFile(filepath.Join(directory, "proxy", "legacy.conf"), data, 0600)
		}
		if err != nil && (!os.IsNotExist(err) || r.state.Completed[6]) {
			return err
		}
	}
	ids, err := r.compose(ctx, "--profile", "subscription", "ps", "-a", "-q")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(ids)) != "" {
		args := append([]string{"inspect", "--format", `{{.Name}} {{.Image}} {{.Config.Image}}`}, strings.Fields(string(ids))...)
		data, e := r.Runner.Run(ctx, nil, "docker", args...)
		if e != nil {
			return e
		}
		if e = writeFile(filepath.Join(directory, "images.txt"), data, 0600); e != nil {
			return e
		}
	}
	manifest := backupManifest{Version: 1, Created: time.Now().UTC(), Reason: reason, Backend: r.state.BackendTag, Files: map[string]string{}}
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(directory, path)
		if e != nil {
			return e
		}
		hash, e := fileHash(path)
		if e != nil {
			return e
		}
		manifest.Files[filepath.ToSlash(rel)] = hash
		return nil
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(directory, "manifest.json"), data, 0600)
}

func (r *Real) backupDirectory(name string) (string, error) {
	if !backupNamePattern.MatchString(name) {
		return "", errors.New("enter a backup directory name from the list")
	}
	dir := filepath.Join(r.Root, name)
	if err := noSymlinkParents(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (r *Real) verifyBackup(name string) (backupManifest, error) {
	var m backupManifest
	dir, err := r.backupDirectory(name)
	if err != nil {
		return m, err
	}
	if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); err != nil {
		return m, errors.New("backup is incomplete")
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Version != 1 || len(m.Files) == 0 {
		return m, errors.New("unsupported or empty backup manifest")
	}
	for _, required := range []string{"database.dump", "compose.json", ".env", "panel.json", "state.key"} {
		if m.Files[required] == "" {
			return m, fmt.Errorf("backup is missing %s", required)
		}
	}
	for name, hash := range m.Files {
		if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
			return m, errors.New("unsafe backup manifest path")
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err = noSymlinkParents(path); err != nil {
			return m, err
		}
		actual, e := fileHash(path)
		if e != nil {
			return m, e
		}
		if actual != hash {
			return m, fmt.Errorf("backup checksum mismatch: %s", name)
		}
	}
	s, err := loadState(statePath(dir))
	if err == nil && protectionMode(s.Params) == "tinyauth" {
		if m.Files["tinyauth-data.tar"] == "" {
			return m, errors.New("TinyAuth data missing from backup")
		}
		if err = validateAuthArchive(filepath.Join(dir, "tinyauth-data.tar")); err != nil {
			return m, err
		}
	}
	if err == nil && s.Params.RemoteSubscription == nil {
		if m.Files["subscription.env"] == "" {
			return m, errors.New("subscription environment missing from backup")
		}
		if s.LayoutVersion >= 1 && m.Files["subscription/docker-compose.yml"] == "" {
			return m, errors.New("subscription Compose missing from backup")
		}
	}
	return m, err
}

func (r *Real) listBackups() (string, error) {
	entries, err := os.ReadDir(r.Root)
	if err != nil {
		return "", err
	}
	rows := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || !backupNamePattern.MatchString(entry.Name()) {
			continue
		}
		data, e := os.ReadFile(filepath.Join(r.Root, entry.Name(), "manifest.json"))
		var m backupManifest
		if e != nil || json.Unmarshal(data, &m) != nil {
			rows = append(rows, entry.Name()+" · incomplete/legacy")
			continue
		}
		state := "complete"
		if _, e = os.Stat(filepath.Join(r.Root, entry.Name(), "COMPLETE")); e != nil {
			state = "incomplete"
		}
		rows = append(rows, fmt.Sprintf("%s · %s · %s · backend %s · %s", entry.Name(), m.Created.Format(time.RFC3339), m.Reason, m.Backend, state))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(rows)))
	if len(rows) == 0 {
		return "No backups / Резервных копий нет.", nil
	}
	return strings.Join(rows, "\n"), nil
}

func (r *Real) deleteBackup(name string) error {
	dir, err := r.backupDirectory(name)
	if err != nil {
		return err
	}
	if r.state.RecoveryBackup == dir {
		return errors.New("cannot delete the active recovery backup")
	}
	// Reject symlinks even inside this explicitly selected managed directory.
	if err = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("backup contains a symlink")
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (r *Real) restoreBackup(ctx context.Context, name string) error {
	if r.state.FirewallPending {
		return errors.New("confirm SSH access and complete the pending firewall operation before restoring")
	}
	if _, err := r.verifyBackup(name); err != nil {
		return err
	}
	directory, _ := r.backupDirectory(name)
	saved, err := loadState(statePath(directory))
	if err != nil {
		return err
	}
	if saved.Params.PanelDomain != r.state.Params.PanelDomain || saved.Params.SubscriptionDomain != r.state.Params.SubscriptionDomain || saved.ProxyRuntime != r.state.ProxyRuntime || saved.Params.Proxy != r.state.Params.Proxy || protectionMode(saved.Params) != protectionMode(r.state.Params) {
		return errors.New("restore requires the same domains, protection and proxy runtime; migrate separately")
	}
	if saved.LayoutVersion != r.state.LayoutVersion {
		return errors.New("backup layout differs; migrate separately")
	}
	input, ok := r.Runner.(StreamInputRunner)
	if !ok {
		return errors.New("runner does not support streaming restore")
	}
	// Do not stop a healthy panel unless a fresh recovery snapshot exists.
	current, err := r.backup(ctx, "pre-restore")
	if err != nil {
		return err
	}
	r.state.RecoveryBackup = current
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return err
	}
	stop := []string{"--profile", "subscription", "stop", "remnawave"}
	if saved.Params.RemoteSubscription == nil && saved.LayoutVersion == 0 {
		stop = append(stop, "subscription")
	}
	if saved.Params.RemoteSubscription == nil && saved.LayoutVersion >= 1 {
		if _, err = r.subscriptionCompose(ctx, "stop", "subscription"); err != nil {
			return err
		}
	}
	if _, err = r.compose(ctx, stop...); err != nil {
		return err
	}
	dump, err := os.Open(filepath.Join(directory, "database.dump"))
	if err != nil {
		return err
	}
	defer dump.Close()
	// Restoring the matching database snapshot avoids running an old application
	// against a schema migrated by a newer version. Never run reverse migrations.
	if err = input.RunFrom(ctx, dump, "docker", r.composeArgs("exec", "-T", "remnawave-db", "pg_restore", "-U", "postgres", "-d", "postgres", "--clean", "--if-exists", "--single-transaction", "--exit-on-error")...); err != nil {
		return fmt.Errorf("restore interrupted; panel remains stopped; recovery backup %s: %w", current, err)
	}
	for _, file := range []string{"compose.json", ".env", "subscription.env", "tinyauth.env", "mfa.env", "mfa.Caddyfile"} {
		data, e := os.ReadFile(filepath.Join(directory, file))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		target := filepath.Join(r.Stack, file)
		if file == "compose.json" {
			target = r.stackComposePath()
		}
		if file == "subscription.env" {
			target = r.subEnvPath()
		}
		if e = writeFile(target, data, 0600); e != nil {
			return e
		}
	}
	if saved.LayoutVersion >= 1 && saved.Params.RemoteSubscription == nil {
		data, e := os.ReadFile(filepath.Join(directory, "subscription", "docker-compose.yml"))
		if e != nil {
			return e
		}
		if e = writeFile(r.subComposePath(), data, 0600); e != nil {
			return e
		}
	}
	if _, err = os.Stat(filepath.Join(directory, "proxy")); err == nil && saved.ProxyRuntime == "docker" {
		if err = copyTree(filepath.Join(directory, "proxy"), r.Stack); err != nil {
			return err
		}
	}
	if saved.ProxyRuntime != "docker" {
		data, e := os.ReadFile(filepath.Join(directory, "proxy", "legacy.conf"))
		if e != nil {
			return e
		}
		if e = writeFile(r.removalProxyPath(), data, 0600); e != nil {
			return e
		}
	}
	saved.NodeSSH = r.state.NodeSSH
	saved.RecoveryBackup = current
	r.state = saved
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return err
	}
	if protectionMode(saved.Params) == "mfa" {
		data, e := os.ReadFile(filepath.Join(directory, "mfa-users.json"))
		if e != nil {
			return e
		}
		if e = r.startProtection(ctx); e != nil {
			return e
		}
		if _, e = r.compose(ctx, "stop", "mfa-gateway"); e != nil {
			return e
		}
		if _, e = r.composeInput(ctx, data, "run", "--rm", "--no-deps", "-T", "--interactive", "--entrypoint", "sh", "mfa-gateway", "-c", "umask 077; cat > /data/users.json"); e != nil {
			return e
		}
	}
	if protectionMode(saved.Params) == "tinyauth" {
		if err = r.restoreTinyAuth(ctx, directory); err != nil {
			return err
		}
	}
	if _, err = r.compose(ctx, "--profile", "subscription", "up", "-d", "--wait", "--wait-timeout", "240"); err != nil {
		return err
	}
	if saved.LayoutVersion >= 1 && saved.Params.RemoteSubscription == nil {
		if _, err = r.subscriptionCompose(ctx, "up", "-d"); err != nil {
			return err
		}
	}
	if err = r.waitAPI(ctx); err != nil {
		return err
	}
	if saved.Params.RemoteSubscription == nil {
		if err = r.waitSubscription(ctx); err != nil {
			return err
		}
	}
	if saved.LayoutVersion >= officialLayout {
		_, err = r.proxyCompose(ctx, saved.Params.Proxy, "up", "-d", "--force-recreate")
	} else {
		err = r.proxyLifecycle(ctx, "restart")
	}
	if err != nil {
		return err
	}
	if err = r.waitHTTPS(ctx); err != nil {
		return err
	}
	r.state.RecoveryBackup = ""
	return saveState(statePath(r.Root), r.state)
}

func (r *Real) composeInput(ctx context.Context, data []byte, args ...string) ([]byte, error) {
	return r.Runner.Run(ctx, data, "docker", r.composeArgs(args...)...)
}
