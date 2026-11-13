package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPanelFirewallRepairPersistsAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"success", "syntax-error", "apply-error", "unowned"} {
		t.Run(scenario, func(t *testing.T) {
			r := NewReal()
			r.Root = t.TempDir()
			r.state.Params = testParams()
			r.state.Ready = true
			r.state.FirewallOwned = scenario != "unowned"
			path := filepath.Join(r.Root, "firewall-active.nft")
			old := "previous policy\n"
			if err := os.WriteFile(path, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			applies := 0
			r.Runner = &fakeRunner{response: func(call string, input []byte) ([]byte, error) {
				if call == "nft list table inet "+firewallTable {
					return []byte(old), nil
				}
				if call == "nft -c -f -" && scenario == "syntax-error" {
					return nil, errors.New("syntax")
				}
				if call == "nft -f -" {
					applies++
					if scenario == "apply-error" && applies == 1 {
						return nil, errors.New("apply")
					}
				}
				if !strings.HasPrefix(string(input), "delete table inet "+firewallTable+"\n") {
					t.Fatal("unscoped firewall mutation")
				}
				return nil, nil
			}}
			err := r.repairPanelFirewall(context.Background())
			if (err == nil) != (scenario == "success") {
				t.Fatal("unexpected result", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				if string(data) != FirewallRules(r.state.Params.SSHPorts, false) || applies != 1 {
					t.Fatal("updated policy not persisted")
				}
			} else if string(data) != old {
				t.Fatal("old policy not restored", string(data))
			}
			if scenario == "apply-error" && applies != 2 {
				t.Fatal("live rollback not attempted")
			}
		})
	}
}

func TestWritePanelFirewallFixture(t *testing.T) {
	dir := os.Getenv("RM_PANEL_FIREWALL_FIXTURE")
	if dir == "" {
		t.Skip("opt-in network fixture export")
	}
	rules := FirewallRules([]int{2222}, false)
	old := strings.ReplaceAll(rules, "  ct status dnat tcp dport { 80, 443 } ct original proto-dst { 80, 443 } accept\n", "")
	for name, data := range map[string]string{"new.nft": rules, "old.nft": old, "caddy.nft": FirewallRules([]int{2222}, true)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
