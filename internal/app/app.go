package app

import (
	"context"
	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/ui"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
)

func Run(ctx context.Context, path string) error {
	store := config.Store{Path: path}
	cfg, err := store.Load()
	if err != nil {
		return err
	}
	model := ui.New(ctx, store, cfg, updater.Checker{Cache: filepath.Join(filepath.Dir(path), "update-cache.json")}, installer.Demo{})
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}
