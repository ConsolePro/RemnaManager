package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/ui"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
	"time"
)

var ErrRestart = errors.New("restart manager")

func Run(ctx context.Context, path string) error {
	return RunMode(ctx, path, false)
}

func RunMode(ctx context.Context, path string, demo bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	store := config.Store{Path: path}
	cfg, err := store.Load()
	if err != nil {
		return err
	}
	var service services.PanelInstaller = installer.Demo{}
	var real *installer.Real
	if !demo {
		real = installer.NewReal()
		defer func() { cancel(); _ = real.Close() }()
		service = real
	}
	cacheControl := &updater.CacheControl{}
	defer cacheControl.Close()
	model := ui.New(ctx, store, cfg, updater.Checker{Cache: filepath.Join(filepath.Dir(path), "update-cache.json"), CacheControl: cacheControl}, service)
	result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if updated, ok := result.(ui.Model); err == nil && ok {
		if plan, requested := updated.ManagerRemovalRequested(); requested {
			cancel()
			cacheControl.Close()
			if real == nil {
				return errors.New("manager removal unavailable in demo mode")
			}
			if err := real.Close(); err != nil {
				return err
			}
			removal, cancelRemoval := context.WithTimeout(context.Background(), time.Minute)
			defer cancelRemoval()
			if err := real.RemoveManager(removal, plan); err != nil {
				return err
			}
			if cfg.Language == "en" {
				fmt.Println("RemnaManager removed. Panel, nodes and component state preserved.")
			} else {
				fmt.Println("RemnaManager удалён. Панель, ноды и состояние компонентов сохранены.")
			}
			return nil
		}
	}
	if updated, ok := result.(ui.Model); err == nil && ok && updated.RestartRequested() {
		return ErrRestart
	}
	return err
}
