package installer

import (
	"context"
	"errors"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Demo struct{}

func (Demo) Check(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	docker := "not found (not required for demo)"
	if _, e := exec.LookPath("docker"); e == nil {
		docker = "found (no commands executed)"
	}
	return runtime.GOOS + "/" + runtime.GOARCH + " · Docker: " + docker, nil
}
func (Demo) Stages() [][2]string {
	return [][2]string{
		{"Проверка сервера", "Server check"}, {"Подготовка окружения", "Environment preparation"},
		{"Проверка Docker", "Docker check"}, {"Создание конфигурации", "Configuration"},
		{"Настройка HTTPS", "HTTPS setup"}, {"Запуск контейнеров", "Container startup"},
		{"Проверка сервисов", "Service check"},
	}
}
func (Demo) Step(ctx context.Context, stage int, p services.PanelParameters) error {
	if stage < 0 || stage >= len((Demo{}).Stages()) {
		return errors.New("invalid demo stage")
	}
	if !ValidDomain(p.PanelDomain) || !ValidDomain(p.SubscriptionDomain) || p.PanelDomain == p.SubscriptionDomain {
		return errors.New("invalid or identical domains")
	}
	timer := time.NewTimer(450 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func ValidDomain(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
