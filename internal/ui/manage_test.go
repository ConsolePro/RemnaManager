package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type managedFake struct {
	liveFake
	requests []services.PanelRequest
}

func (f *managedFake) Manage(ctx context.Context, request services.PanelRequest) (services.PanelResult, error) {
	if ctx.Err() != nil {
		return services.PanelResult{}, ctx.Err()
	}
	f.requests = append(f.requests, request)
	return services.PanelResult{Text: "Operation complete"}, nil
}

func TestManagementRequiresConfirmationAndHidesToken(t *testing.T) {
	m := testModel(t, true)
	service := &managedFake{}
	m.service = service
	next, cmd := m.openPanelOperation(operation("Сменить API-токен", "Change token", "panel-token"))
	m = next.(Model)
	if m.screen != "panelInput" || len(service.requests) != 0 {
		t.Fatal("operation ran without input")
	}
	_ = cmd // Cursor blink is independent from submitting the form.
	m.inputs[0].SetValue("replacement-token-123456789")
	press(&m, tea.KeyEnter)
	if m.screen != "panelConfirm" || strings.Contains(m.View(), "replacement-token") {
		t.Fatal("token leaked in confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "panelResult" || len(service.requests) != 1 || m.panelRequest.Value != "" || m.inputs[0].Value() != "" {
		t.Fatal("operation did not complete or retained secret input")
	}
}
func TestCancelledManagementIgnoresLateResult(t *testing.T) {
	m := testModel(t, true)
	m.service = &managedFake{}
	next, cmd := m.openPanelOperation(operation("Статус", "Status", "panel-status"))
	m = next.(Model)
	ctx := m.installCtx
	press(&m, tea.KeyEsc)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("operation was not cancelled")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "notice" {
		t.Fatal("late result reopened operation")
	}
}
func TestPanelMenuHasNoPendingOperations(t *testing.T) {
	var check func([]item)
	check = func(items []item) {
		for _, item := range items {
			if item.action == "pending" {
				t.Fatal("unimplemented panel action", item.ru)
			}
			check(item.children)
		}
	}
	check(menu()[0].children)
}

func TestPanelRemovalRequiresTypedConsentAndFinalConfirmation(t *testing.T) {
	m := testModel(t, true)
	service := &managedFake{}
	m.service = service
	next, _ := m.openPanelOperation(operation("Удалить панель и данные", "Remove panel and data", "panel-remove"))
	m = next.(Model)
	if m.screen != "panelInput" || !strings.Contains(m.body(), "безвозвратно") {
		t.Fatal(m.body())
	}
	press(&m, tea.KeyEnter)
	if m.screen != "panelInput" || len(service.requests) != 0 {
		t.Fatal("empty confirmation accepted")
	}
	m.inputs[0].SetValue("DELETE")
	press(&m, tea.KeyEnter)
	if m.screen != "panelConfirm" || len(service.requests) != 0 || !strings.Contains(m.body(), "firewall") {
		t.Fatal("final confirmation missing", m.body())
	}
	press(&m, tea.KeyEsc)
	if len(service.requests) != 0 {
		t.Fatal("cancelled removal executed")
	}
}

func TestOfficialImageConfirmationCanGoBackWithoutInput(t *testing.T) {
	m := testModel(t, true)
	m.service = &managedFake{}
	next, _ := m.openPanelOperation(operation("Кастомизация", "Customize", "sub-custom"))
	m = next.(Model)
	press(&m, tea.KeyEnter)
	if m.screen != "panelConfirm" {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyEsc)
	if m.screen != "panelCustom" {
		t.Fatal("did not return to image choices")
	}
}

func TestManagementOutputWrapsAndScrolls(t *testing.T) {
	m := testModel(t, true)
	m.width = 80
	m.height = 24
	m.screen = "panelResult"
	m.panelResult = strings.Repeat("long-log-entry-", 60) + "\nlast-log-line"
	body := m.viewportBody()
	if strings.Count(body, "\n") < 8 {
		t.Fatal("log lines were clipped instead of wrapped")
	}
	for i := 0; i < 4; i++ {
		press(&m, tea.KeyPgDown)
	}
	if m.scroll < 8 {
		t.Fatal("cannot scroll wrapped log lines")
	}
}

func TestPanelProtectionRequiresDomainAndConfirmation(t *testing.T) {
	m := testModel(t, true)
	service := &managedFake{}
	m.service = service
	next, _ := m.openPanelOperation(operation("Защита панели", "Panel protection", "panel-protection"))
	m = next.(Model)
	press(&m, tea.KeyDown)
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("auth.panel.example.com")
	press(&m, tea.KeyEnter)
	if m.screen != "panelConfirm" || len(service.requests) != 0 {
		t.Fatal("protection ran without confirmation")
	}
	press(&m, tea.KeyEsc)
	if m.screen != "panelInput" {
		t.Fatal("cannot correct authentication domain")
	}
	press(&m, tea.KeyEnter)
	cmd := press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(service.requests) != 1 || service.requests[0].Protection != "tinyauth" || service.requests[0].Value != "auth.panel.example.com" {
		t.Fatal("incorrect protection request")
	}
}
