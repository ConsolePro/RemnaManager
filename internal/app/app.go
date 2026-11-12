package app

import (
	"context"
	"errors"
	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/ui"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
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
	if !demo {
		real := installer.NewReal()
		defer func() { cancel(); _ = real.Close() }()
		service = real
	}
	model := ui.New(ctx, store, cfg, updater.Checker{Cache: filepath.Join(filepath.Dir(path), "update-cache.json")}, service)
	result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if updated, ok := result.(ui.Model); err == nil && ok && updated.RestartRequested() {
		return ErrRestart
	}
	return err
}
