package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (r *Real) changeDockerProtection(ctx context.Context, mode, domain string) error {
	if r.state.ProtectionPending && r.state.ProtectionBackup != "" {
		directory := r.state.ProtectionBackup
		if filepath.Dir(directory) != filepath.Clean(r.Root) {
			return errors.New("invalid protection recovery path")
		}
		if _, err := r.verifyBackup(filepath.Base(directory)); err != nil {
			return err
		}
		old, err := loadState(statePath(directory))
		if err != nil {
			return err
		}
		r.state = old
		if err = r.configure(); err != nil {
			return err
		}
		if err = r.startProtection(ctx); err != nil {
			return err
		}
		if err = r.configureProxy(ctx); err != nil {
			return err
		}
		if err = r.checkPanelProtection(ctx); err != nil {
			return err
		}
		return saveState(statePath(r.Root), old)
	}
	old := r.state
	p := old.Params
	p.Protection = mode
	p.AuthDomain = strings.ToLower(strings.TrimSpace(domain))
	if err := ValidateParameters(p); err != nil {
		return err
	}
	backup, err := r.backup(ctx, "pre-security-change")
	if err != nil {
		return err
	}
	r.state.Params = p
	if err = ensureProtection(&r.state); err != nil {
		r.state = old
		return err
	}
	r.state.ProtectionPending = true
	r.state.ProtectionBackup = backup
	if err = saveState(statePath(r.Root), r.state); err != nil {
		r.state = old
		return err
	}
	apply := func() error {
		if e := r.configure(); e != nil {
			return e
		}
		if _, e := r.compose(ctx, "config", "--quiet"); e != nil {
			return e
		}
		if e := r.startProtection(ctx); e != nil {
			return e
		}
		if e := r.configureProxy(ctx); e != nil {
			return e
		}
		if e := r.checkPanelProtection(ctx); e != nil {
			return e
		}
		return nil
	}
	if err = apply(); err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r.state = old
		restoreErr := r.configure()
		if restoreErr == nil {
			restoreErr = r.startProtection(recovery)
		}
		if restoreErr == nil {
			restoreErr = r.configureProxy(recovery)
		}
		if restoreErr == nil {
			restoreErr = r.checkPanelProtection(recovery)
		}
		if restoreErr != nil {
			r.state.ProtectionPending = true
			r.state.ProtectionBackup = backup
		}
		return errors.Join(fmt.Errorf("protection change failed; recovery snapshot: %s: %w", backup, err), restoreErr, saveState(statePath(r.Root), r.state))
	}
	r.state.ProtectionPending = false
	r.state.ProtectionBackup = ""
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return err
	}
	// Dormant authentication volumes are deliberately retained, including MFA.
	return nil
}
