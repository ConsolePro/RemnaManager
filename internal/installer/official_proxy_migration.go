package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
)

func (r *Real) changeOfficialProxy(ctx context.Context, proxy string) error {
	if proxy != "nginx" && proxy != "caddy" {
		return errors.New("proxy must be nginx or caddy")
	}
	if proxy == r.state.Params.Proxy {
		return nil
	}
	old := r.state
	backup, err := r.backup(ctx, "pre-proxy-migration")
	if err != nil {
		return err
	}
	if _, err = r.Runner.Run(ctx, setupScript, "bash", "-s", "--", "docker-proxy", runtime.GOARCH); err != nil {
		return err
	}
	journal := old
	journal.ProxyRecovery = backup
	journal.ProxyTarget = proxy
	if err = saveState(statePath(r.Root), journal); err != nil {
		return err
	}
	if _, err = r.proxyCompose(ctx, old.Params.Proxy, "stop"); err != nil {
		return err
	}
	r.state.Params.Proxy = proxy
	apply := func() error {
		if e := r.configureOfficialProxy(ctx); e != nil {
			return e
		}
		return r.waitHTTPS(ctx)
	}
	if err = apply(); err == nil {
		err = saveState(statePath(r.Root), r.state)
	}
	if err == nil {
		return nil
	}
	recovery, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, stopErr := r.proxyCompose(recovery, proxy, "stop")
	restoreErr := copyTree(filepath.Join(backup, "proxy"), r.Stack)
	r.state = old
	if restoreErr == nil {
		_, restoreErr = r.proxyCompose(recovery, old.Params.Proxy, "up", "-d", "--force-recreate")
	}
	if restoreErr == nil {
		restoreErr = r.checkPanelProtection(recovery)
	}
	if restoreErr != nil {
		r.state.ProxyRecovery = backup
		r.state.ProxyTarget = proxy
	}
	return errors.Join(fmt.Errorf("proxy migration failed; backup: %s: %w", backup, err), stopErr, restoreErr, saveState(statePath(r.Root), r.state))
}
