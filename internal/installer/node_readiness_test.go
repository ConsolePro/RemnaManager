package installer

import (
	"context"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeReadinessIdentifiesBlockingStateBeforeSSH(t *testing.T) {
	tests := []struct {
		name, wantRU, wantEN string
		change               func(*State)
	}{
		{"incomplete", "установка панели не отмечена", "not marked complete", func(s *State) { s.Ready = false }},
		{"ssh", "не подтверждён SSH-доступ", "has not been confirmed", func(s *State) { s.Ready = false; s.FirewallPending = true }},
		{"database", "восстановление базы данных", "recover the database", func(s *State) { s.RecoveryBackup = "/private/backup" }},
		{"subscription", "Страница подписки → Установка / запуск", "Subscription page → Install / start", func(s *State) { s.SubRecovery = "/private/sub-recovery" }},
		{"protection", "не завершена настройка защиты", "protection setup is pending", func(s *State) { s.ProtectionPending = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, runner := managementFixture(t)
			tt.change(&r.state)
			if err := saveState(statePath(r.Root), r.state); err != nil {
				t.Fatal(err)
			}
			_, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-install-scan"})
			if err == nil || !strings.Contains(services.ErrorText(err, "ru"), tt.wantRU) || !strings.Contains(services.ErrorText(err, "en"), tt.wantEN) {
				t.Fatal(err)
			}
			if len(runner.calls) != 0 {
				t.Fatal("SSH or host operation ran before readiness check", runner.calls)
			}
		})
	}
	if err := nodeReadinessError(State{Ready: true}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeOptionalFieldsCanBeEmpty(t *testing.T) {
	p := services.NodeInstallParameters{Name: "Node-01", Address: "node.example.com", Domain: "cover.example.com", SSH: services.NodeSSH{Host: "node.example.com", Port: 22}}
	if err := ValidateNodeInstall(p); err != nil {
		t.Fatal("empty optional fields rejected", err)
	}
}
