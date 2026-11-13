package ui

import (
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWizard36OrderedBranches(t *testing.T) {
	count := 0
	for _, remote := range []bool{false, true} {
		for _, proxy := range []int{0, 1} {
			methods := []int{0}
			if proxy == 0 {
				methods = []int{0, 1, 2, 3, 4}
			}
			for _, method := range methods {
				for protection := range protectionChoices {
					count++
					m := testModel(t, true)
					m.service = &managedFake{}
					m.prepareInstallInputs()
					m.screen = "installLayout"
					if remote {
						press(&m, tea.KeyDown)
					}
					press(&m, tea.KeyEnter)
					if m.screen != "domains" {
						t.Fatal("domains must follow placement", m.screen)
					}
					m.inputs[0].SetValue("panel.example.com")
					m.inputs[1].SetValue("sub.example.com")
					for range len(m.inputs) {
						press(&m, tea.KeyEnter)
					}
					if remote {
						if m.screen != "panelRemoteInput" || len(m.inputs) != 4 || m.inputs[1].Value() != "22" || m.inputs[2].Value() != "root" || m.inputs[3].EchoMode != textinput.EchoPassword {
							t.Fatal("password SSH form missing", m.screen)
						}
						for i, value := range []string{"192.0.2.40", "2222", "root", "fixture-ssh-password"} {
							m.inputs[i].SetValue(value)
						}
						for range 4 {
							press(&m, tea.KeyEnter)
						}
						next, _ := m.receivePanelResult(panelResultMsg{run: m.run, result: services.PanelResult{Remote: services.SubscriptionRemote{User: "root", Proxy: "caddy", Password: "fixture-ssh-password", SSH: services.NodeSSH{Host: "192.0.2.40", Port: 2222, HostKey: "pinned-key"}}, Fingerprint: "SHA256:fixture"}})
						m = next.(Model)
						press(&m, tea.KeyEnter)
					}
					if m.screen != "installOptions" {
						t.Fatal("proxy must follow domains/SSH", m.screen)
					}
					m.proxyChoice = proxy
					press(&m, tea.KeyEnter)
					if proxy == 0 {
						if m.screen != "certificateOptions" {
							t.Fatal("Nginx SSL menu missing")
						}
						for range method {
							press(&m, tea.KeyDown)
						}
						press(&m, tea.KeyEnter)
						if method > 0 {
							if m.screen != "certificateInput" {
								t.Fatal(m.screen)
							}
							if method != 4 && m.inputs[0].EchoMode != textinput.EchoPassword {
								t.Fatal("API token is visible")
							}
							for i := range m.inputs {
								m.inputs[i].SetValue("fixture-ssl-value")
							}
							for range len(m.inputs) {
								press(&m, tea.KeyEnter)
							}
						}
					}
					if m.screen != "protectionOptions" {
						t.Fatal("protection must follow proxy/SSL", m.screen)
					}
					for range protection {
						press(&m, tea.KeyDown)
					}
					cmd := press(&m, tea.KeyEnter)
					if protection == 1 {
						if m.screen != "protectionDetails" {
							t.Fatal(m.screen)
						}
						cmd = press(&m, tea.KeyEnter)
					}
					if m.screen != "installPreflight" || cmd == nil {
						t.Fatal("checks must precede confirmation", m.screen)
					}
					next, _ := m.Update(cmd())
					m = next.(Model)
					if m.screen != "confirm" {
						t.Fatal(m.screen, m.problem)
					}
					p := m.parameters()
					if p.PanelDomain != "panel.example.com" || p.SubscriptionDomain != "sub.example.com" || p.Protection != protectionChoices[protection] {
						t.Fatal("draft lost", p)
					}
					if remote && (p.RemoteSubscription == nil || p.RemoteSubscription.Proxy != p.Proxy || p.RemoteSubscription.SSH.Port != 2222) {
						t.Fatal("remote proxy or port lost", p)
					}
					if strings.Contains(m.body(), "fixture-ssh-password") || strings.Contains(m.body(), "fixture-ssl-value") {
						t.Fatal("secret in confirmation")
					}
					if proxy == 1 && p.Certificate.Method != "" {
						t.Fatal("Caddy retained Nginx SSL settings")
					}
					press(&m, tea.KeyEsc)
					if m.screen != "protectionOptions" || m.parameters().PanelDomain != "panel.example.com" {
						t.Fatal("back lost input")
					}
				}
			}
		}
	}
	if count != 36 {
		t.Fatal(count)
	}
}
