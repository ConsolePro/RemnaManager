package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/updater"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type managerUpdates interface {
	Prepare(context.Context, string, bool) (updater.Plan, error)
	Apply(context.Context, updater.Plan) error
}
type managerUpdateMsg struct {
	run     int
	plan    updater.Plan
	applied bool
	err     error
}

func (m Model) openManagerUpdate(action string) (tea.Model, tea.Cmd) {
	if m.managerUpdater == nil {
		if !m.liveInstall() {
			m.screen = "notice"
			m.notice = m.l("Обновление менеджера недоступно в деморежиме.", "Manager updates are unavailable in demo mode.")
			return m, nil
		}
		service, err := updater.NewManager()
		if err != nil {
			m.screen = "notice"
			m.notice = m.errorText(err)
			return m, nil
		}
		m.managerUpdater = service
	}
	m.managerApplied = false
	m.problem = ""
	if action == "manager-select" {
		m.inputs = []textinput.Model{newInput("v1.2.3", "")}
		m.focus = 0
		m.screen = "managerInput"
		return m, m.inputs[0].Focus()
	}
	return m.prepareManagerUpdate("", action == "manager-rollback")
}
func (m Model) prepareManagerUpdate(tag string, rollback bool) (tea.Model, tea.Cmd) {
	m.cancelInstall()
	m.run++
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	run, ctx, service := m.run, m.installCtx, m.managerUpdater
	m.screen = "managerProgress"
	m.problem = ""
	return m, func() tea.Msg {
		operation, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		plan, err := service.Prepare(operation, tag, rollback)
		return managerUpdateMsg{run: run, plan: plan, err: err}
	}
}
func (m Model) receiveManagerUpdate(msg managerUpdateMsg) (tea.Model, tea.Cmd) {
	if msg.run != m.run || m.screen != "managerProgress" {
		return m, nil
	}
	m.cancelInstall()
	if msg.err != nil {
		m.problem = m.errorText(msg.err)
		m.screen = "managerResult"
		return m, nil
	}
	m.managerPlan = msg.plan
	m.managerApplied = msg.applied
	if msg.applied {
		m.screen = "managerResult"
	} else {
		m.screen = "managerConfirm"
	}
	return m, nil
}
func (m Model) updateManagerScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		switch m.screen {
		case "managerInput":
			tag := strings.TrimSpace(m.inputs[0].Value())
			if !updater.TagPattern.MatchString(tag) {
				m.problem = m.l("Введите версию вида v1.2.3.", "Enter a version such as v1.2.3.")
				return m, nil
			}
			return m.prepareManagerUpdate(tag, false)
		case "managerConfirm":
			m.cancelInstall()
			m.run++
			m.installCtx, m.installCancel = context.WithCancel(m.ctx)
			run, ctx, service, plan := m.run, m.installCtx, m.managerUpdater, m.managerPlan
			m.screen = "managerProgress"
			return m, func() tea.Msg {
				operation, cancel := context.WithTimeout(ctx, 5*time.Minute)
				defer cancel()
				err := service.Apply(operation, plan)
				return managerUpdateMsg{run: run, plan: plan, applied: err == nil, err: err}
			}
		case "managerResult":
			if m.managerApplied {
				m.restartRequested = true
				return m, tea.Quit
			}
			m.screen = "menu"
			return m, nil
		}
	}
	if m.screen == "managerInput" {
		var cmd tea.Cmd
		m.inputs[0], cmd = m.inputs[0].Update(msg)
		return m, cmd
	}
	return m, nil
}
func (m Model) managerUpdateView() string {
	title := accent.Render(m.l("Обновление менеджера", "Manager update"))
	switch m.screen {
	case "managerInput":
		return title + "\n\n" + m.l("Версия релиза:", "Release version:") + "\n" + m.inputs[0].View() + "\n\nEnter · Esc"
	case "managerProgress":
		return title + "\n\n" + m.l("Проверка релиза и приложения…", "Verifying release and executable…")
	case "managerConfirm":
		return title + fmt.Sprintf("\n\n%s → %s\nSHA256: %s\n\n", m.managerPlan.Current, m.managerPlan.Version, m.managerPlan.SHA256) + m.l("Предыдущий исполняемый файл будет сохранён для отката.\nНастройки и компоненты панели сохраняются.\nEnter — установить · Esc — назад", "The previous executable will be retained for rollback.\nSettings and panel components are preserved.\nEnter — install · Esc — back")
	case "managerResult":
		if m.managerApplied {
			return title + "\n\n" + m.l("Версия установлена и запуск проверен: ", "Version installed and startup verified: ") + m.managerPlan.Version + "\n\n" + m.l("Enter — перезапустить менеджер", "Enter — restart manager")
		}
		return title + "\n\n" + m.l("Обновление не завершено.\nEnter — в меню", "Update was not completed.\nEnter — menu")
	}
	return ""
}
func (m Model) RestartRequested() bool { return m.restartRequested }
