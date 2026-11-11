package ui

import (
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeReadinessErrorUsesSelectedLanguageAndKeepsEnteredFields(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			m := testModel(t, true)
			m.cfg.Language = language
			m.screen, m.nodeAction = "nodesProgress", "node-install-add"
			m.nodeInstallParams = services.NodeInstallParameters{Name: "Node-01", Address: "node.example.com", Domain: "cover.example.com", SSH: services.NodeSSH{Port: 22}}
			err := services.LocalizedError("Подтвердите SSH-доступ после установки панели.", "Confirm SSH access after panel installation.")
			next, _ := m.receiveNodeResult(nodeResultMsg{run: m.run, action: "node-install-scan", err: err})
			m = next.(Model)
			want := "Подтвердите SSH-доступ"
			if language == "en" {
				want = "Confirm SSH access"
			}
			if !strings.Contains(m.problem, want) || m.screen != "nodeInstallInput" || m.inputs[0].Value() != "Node-01" || m.inputs[1].Value() != "node.example.com" {
				t.Fatal(m.problem, m.screen)
			}
		})
	}
}
