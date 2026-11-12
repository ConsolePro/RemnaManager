package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type managerRemovalMsg struct {
	run  int
	plan services.ManagerRemovalPlan
	err  error
}

func (m Model) openManagerRemoval() (tea.Model, tea.Cmd) {
	service, ok := m.service.(services.ManagerRemover)
	if !ok {
		m.screen, m.notice = "notice", m.l("Удаление менеджера недоступно в деморежиме.", "Manager removal is unavailable in demo mode.")
		return m, nil
	}
	m.cancelInstall()
	m.run++
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	m.managerRemovalRequested = false
	m.problem, m.scroll = "", 0
	m.screen = "managerRemoveProgress"
	run, ctx := m.run, m.installCtx
	return m, func() tea.Msg {
		operation, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		plan, err := service.PrepareManagerRemoval(operation)
		return managerRemovalMsg{run, plan, err}
	}
}

func (m Model) receiveManagerRemoval(msg managerRemovalMsg) (tea.Model, tea.Cmd) {
	if m.screen != "managerRemoveProgress" || m.run != msg.run {
		return m, nil
	}
	m.cancelInstall()
	if msg.err != nil {
		m.screen, m.problem = "managerRemoveResult", m.errorText(msg.err)
		return m, nil
	}
	m.managerRemovalPlan, m.screen = msg.plan, "managerRemoveConfirm"
	return m, nil
}

func (m Model) updateManagerRemoval(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		if m.screen == "managerRemoveConfirm" {
			m.cancelInstall()
			m.managerRemovalRequested = true
			return m, tea.Quit
		}
		m.screen = "menu"
	}
	return m, nil
}

func (m Model) managerRemovalView() string {
	rows := []string{accent.Render(m.l("Удаление менеджера", "Remove manager")), ""}
	switch m.screen {
	case "managerRemoveProgress":
		return strings.Join(rows, "\n") + m.l("Проверяю установленные файлы…", "Checking installed files…")
	case "managerRemoveResult":
		return strings.Join(rows, "\n") + m.l("Удаление не выполнено. Enter — в меню.", "Removal was not performed. Enter — menu.")
	}
	rows = append(rows, m.l("Будут удалены приложение, его команды, конфигурация, кеш и журналы:", "The executable, its commands, configuration, cache and logs will be removed:"))
	for _, path := range m.managerRemovalPlan.Remove {
		rows = append(rows, "  "+strconv.Quote(path))
	}
	rows = append(rows, "", m.l("Сохраняются панель, БД, подписка и ноды, Docker, прокси, сертификаты и firewall. Состояние компонентов, резервные копии и SSH-ключи управления остаются для повторной установки менеджера.", "The panel, database, subscription, nodes, Docker, proxy, certificates and firewall are preserved. Component state, backups and managed SSH keys remain for manager reinstallation."), "", m.l("Сохраняемые пути:", "Preserved paths:"))
	for _, path := range m.managerRemovalPlan.Preserve {
		rows = append(rows, "  "+strconv.Quote(path))
	}
	rows = append(rows, "", warning.Render(m.l("Enter — удалить менеджер и выйти · Esc — отменить", "Enter — remove manager and exit · Esc — cancel")))
	return strings.Join(rows, "\n")
}

func (m Model) ManagerRemovalRequested() (services.ManagerRemovalPlan, bool) {
	return m.managerRemovalPlan, m.managerRemovalRequested
}
