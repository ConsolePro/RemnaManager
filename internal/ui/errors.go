package ui

import "github.com/ConsolePro/RemnaManager/internal/services"

func (m Model) errorText(err error) string { return services.ErrorText(err, m.cfg.Language) }
