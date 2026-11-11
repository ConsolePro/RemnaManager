package installer

import (
	"context"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Only known setup markers reach the UI. Command output can contain secrets.
var setupActions = map[string][2]string{
	"packages-update":   {"Обновляю список пакетов", "Updating package lists"},
	"packages-install":  {"Устанавливаю зависимости и выбранный прокси", "Installing dependencies and the selected proxy"},
	"packages-ready":    {"Необходимые пакеты уже установлены", "Required packages are already installed"},
	"docker-repository": {"Подключаю официальный репозиторий Docker", "Configuring the official Docker repository"},
	"docker-install":    {"Устанавливаю Docker Engine и Compose", "Installing Docker Engine and Compose"},
	"docker-ready":      {"Docker уже установлен — сохраняю текущую версию", "Docker is already installed — keeping the current version"},
	"docker-start":      {"Запускаю Docker и включаю автозапуск", "Starting Docker and enabling it at boot"},
	"docker-check":      {"Проверяю версии Docker Engine и Compose", "Checking Docker Engine and Compose versions"},
}

type setupProgressWriter struct {
	ctx     context.Context
	line    string
	discard bool
}

func (w *setupProgressWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			if !w.discard {
				if action, ok := setupActions[strings.TrimPrefix(w.line, "@RM_PROGRESS ")]; ok && strings.HasPrefix(w.line, "@RM_PROGRESS ") {
					services.ReportProgress(w.ctx, action[0], action[1])
				}
			}
			w.line, w.discard = "", false
		} else if !w.discard {
			if len(w.line) >= 128 {
				w.line, w.discard = "", true
			} else {
				w.line += string(b)
			}
		}
	}
	return len(data), nil
}
