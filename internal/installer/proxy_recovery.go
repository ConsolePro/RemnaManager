package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (r *Real) recoverProxyMigration(ctx context.Context) error {
	directory := r.state.ProxyRecovery
	if _, err := r.verifyBackup(filepath.Base(directory)); err != nil {
		return err
	}
	if filepath.Dir(directory) != filepath.Clean(r.Root) {
		return errors.New("invalid proxy recovery path")
	}
	old, err := loadState(statePath(directory))
	if err != nil {
		return err
	}
	target := r.state.ProxyTarget
	if target != "nginx" && target != "caddy" {
		return errors.New("invalid proxy recovery target")
	}
	if _, err = r.proxyCompose(ctx, target, "stop"); err != nil {
		return err
	}
	if old.ProxyRuntime == "docker" {
		if err = copyTree(filepath.Join(directory, "proxy"), r.Stack); err != nil {
			return err
		}
	} else {
		data, e := os.ReadFile(filepath.Join(directory, "proxy", "legacy.conf"))
		if e != nil {
			return e
		}
		r.state = old
		if e = writeFile(r.removalProxyPath(), data, 0600); e != nil {
			return e
		}
	}
	r.state = old
	if old.LayoutVersion >= officialLayout {
		_, err = r.proxyCompose(ctx, old.Params.Proxy, "up", "-d", "--force-recreate")
	} else {
		err = r.proxyLifecycle(ctx, "start")
	}
	if err != nil {
		return err
	}
	if err = r.checkPanelProtection(ctx); err != nil {
		return err
	}
	return saveState(statePath(r.Root), old)
}
