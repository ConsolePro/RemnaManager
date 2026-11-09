package installer

import (
	"context"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"testing"
)

func TestValidationAndCancellation(t *testing.T) {
	for _, s := range []string{"panel.example.com", "xn--e1afmkfd.xn--p1ai"} {
		if !ValidDomain(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"https://panel.example.com", "a..com", "-a.com", "a.com/path", "a.com;touch x", "локальный.рф"} {
		if ValidDomain(s) {
			t.Fatal(s)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if (Demo{}).Step(ctx, 0, services.PanelParameters{PanelDomain: "a.example.com", SubscriptionDomain: "b.example.com"}) == nil {
		t.Fatal("cancellation ignored")
	}
}
