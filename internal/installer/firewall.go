package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const firewallTable = "remnamanager_go"
const rollbackUnit = "remnamanager-firewall-rollback"

func DetectSSHPorts(ctx context.Context, runner Runner) ([]int, error) {
	data, e := runner.Run(ctx, nil, "/usr/sbin/sshd", "-T")
	if e != nil {
		return nil, errors.New("cannot read effective SSH configuration (sshd -T); refusing to guess a port")
	}
	seen := map[int]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "port" {
			n, e := strconv.Atoi(fields[1])
			if e == nil && n > 0 && n <= 65535 {
				seen[n] = true
			}
		}
	}
	if fields := strings.Fields(os.Getenv("SSH_CONNECTION")); len(fields) == 4 {
		n, e := strconv.Atoi(fields[3])
		if e == nil && n > 0 && n <= 65535 {
			seen[n] = true
		}
	}
	ports := []int{}
	for n := range seen {
		ports = append(ports, n)
	}
	sort.Ints(ports)
	if len(ports) == 0 {
		return nil, errors.New("no SSH port detected; firewall will not be applied")
	}
	return ports, nil
}

func FirewallRules(ports []int, caddy bool) string {
	values := make([]string, len(ports))
	for i, p := range ports {
		values[i] = strconv.Itoa(p)
	}
	webUDP := ""
	forwardUDP := ""
	if caddy {
		webUDP = "udp dport 443 accept"
		forwardUDP = "ct status dnat udp dport 443 ct original proto-dst 443 accept"
	}
	return fmt.Sprintf(`table inet %s {
 chain input {
  type filter hook input priority -10; policy drop;
  ct state invalid drop
  ct state established,related accept
  iifname "lo" accept
  meta l4proto { 1, 58 } accept
  udp sport 67 udp dport 68 accept
  udp sport 547 udp dport 546 accept
  tcp dport { %s, 80, 443 } accept
  %s
 }
 chain forward {
  type filter hook forward priority -10; policy drop;
  ct state established,related accept
  iifname "docker0" accept
  iifname "br-*" accept
  ct status dnat tcp dport { 80, 443 } ct original proto-dst { 80, 443 } accept
  %s
  oifname "docker0" drop
  oifname "br-*" drop
  ct status dnat drop
 }
}
`, firewallTable, strings.Join(values, ", "), webUDP, forwardUDP)
}

// Repair the managed table and its boot-time policy together. The input policy
// and saved SSH allowlist stay identical; Docker's own tables are untouched.
func (r *Real) repairPanelFirewall(ctx context.Context) error {
	if !r.state.FirewallOwned || !r.state.Ready || r.state.FirewallPending {
		return errors.New("a completed, owned firewall is required")
	}
	previous, err := r.Runner.Run(ctx, nil, "nft", "list", "table", "inet", firewallTable)
	if err != nil {
		return err
	}
	activePath := filepath.Join(r.Root, "firewall-active.nft")
	previousFile, err := os.ReadFile(activePath)
	if err != nil {
		return err
	}
	rules := FirewallRules(r.state.Params.SSHPorts, r.state.Params.Proxy == "caddy")
	replacement := []byte("delete table inet " + firewallTable + "\n" + rules)
	if _, err = r.Runner.Run(ctx, replacement, "nft", "-c", "-f", "-"); err != nil {
		return err
	}
	if err = writeFile(activePath, []byte(rules), 0600); err != nil {
		return err
	}
	if _, err = r.Runner.Run(ctx, replacement, "nft", "-f", "-"); err != nil {
		// nft transactions are atomic. Restore the persisted file too, and use
		// an independent context if cancellation happened during application.
		restoreErr := writeFile(activePath, previousFile, 0600)
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, rollbackErr := r.Runner.Run(rollbackCtx, append([]byte("delete table inet "+firewallTable+"\n"), previous...), "nft", "-f", "-")
		return errors.Join(err, restoreErr, rollbackErr)
	}
	return nil
}

// Refuse unknown input filtering rather than flushing another administrator's
// rules or pretending that our accept overrides a later base-chain drop.
func CheckForeignFirewall(data []byte) error {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if e := json.Unmarshal(data, &doc); e != nil {
		return e
	}
	type entry struct{ Family, Table, Name, Chain, Hook, Policy string }
	chains := map[string]entry{}
	rules := map[string]int{}
	for _, node := range doc.Nftables {
		var v entry
		if raw, ok := node["chain"]; ok {
			if e := json.Unmarshal(raw, &v); e != nil {
				return e
			}
			if v.Hook == "input" && v.Table != firewallTable {
				chains[v.Family+"/"+v.Table+"/"+v.Name] = v
			}
		}
		if raw, ok := node["rule"]; ok {
			if e := json.Unmarshal(raw, &v); e != nil {
				return e
			}
			rules[v.Family+"/"+v.Table+"/"+v.Chain]++
		}
	}
	for key, chain := range chains {
		if chain.Policy == "drop" || rules[key] > 0 {
			return fmt.Errorf("existing input firewall %s needs explicit migration; no rules were changed", key)
		}
	}
	return nil
}
func (r *Real) checkFirewall(ctx context.Context) error {
	if _, e := r.Runner.Run(ctx, nil, "systemctl", "is-active", "--quiet", "firewalld"); e == nil {
		return errors.New("active firewalld requires explicit migration")
	}
	if data, e := r.Runner.Run(ctx, nil, "ufw", "status"); e == nil && strings.Contains(string(data), "Status: active") {
		return errors.New("active UFW requires explicit migration")
	}
	for _, command := range []string{"iptables-legacy", "ip6tables-legacy"} {
		if data, e := r.Runner.Run(ctx, nil, command, "-S", "INPUT"); e == nil && strings.TrimSpace(string(data)) != "-P INPUT ACCEPT" {
			return errors.New("existing legacy iptables input rules require explicit migration")
		}
	}
	data, e := r.Runner.Run(ctx, nil, "nft", "-j", "list", "ruleset")
	if e != nil {
		return e
	}
	return CheckForeignFirewall(data)
}

func (r *Real) applyFirewall(ctx context.Context) error {
	if r.state.Ready {
		_, e := r.Runner.Run(ctx, nil, "nft", "list", "table", "inet", firewallTable)
		return e
	}
	if r.state.FirewallPending && time.Now().Before(r.state.RollbackAt) {
		return errors.New("a firewall rollback timer is already armed; confirm SSH access or wait for rollback")
	}
	if e := r.checkFirewall(ctx); e != nil {
		return e
	}
	detected, e := DetectSSHPorts(ctx, r.Runner)
	if e != nil {
		return e
	}
	for _, port := range detected {
		found := false
		for _, keep := range r.state.Params.SSHPorts {
			if keep == port {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("SSH port %d is not in the allowlist", port)
		}
	}
	// Only our table is replaced. Docker/other tables are never flushed.
	old, existsErr := r.Runner.Run(ctx, nil, "nft", "list", "table", "inet", firewallTable)
	if existsErr == nil && !r.state.FirewallOwned {
		return errors.New("firewall table name is already in use without an ownership record")
	}
	replacement := FirewallRules(r.state.Params.SSHPorts, r.state.Params.Proxy == "caddy")
	rollback := "delete table inet " + firewallTable + "\n"
	if existsErr == nil {
		replacement = "delete table inet " + firewallTable + "\n" + replacement
		rollback += string(old)
	}
	if _, e = r.Runner.Run(ctx, []byte(replacement), "nft", "-c", "-f", "-"); e != nil {
		return e
	}
	rollbackPath := filepath.Join(r.Root, "firewall-rollback.nft")
	activePath := filepath.Join(r.Root, "firewall-active.nft")
	if e = writeFile(rollbackPath, []byte(rollback), 0600); e != nil {
		return e
	}
	if e = writeFile(activePath, []byte(FirewallRules(r.state.Params.SSHPorts, r.state.Params.Proxy == "caddy")), 0600); e != nil {
		return e
	}
	// A separate systemd timer survives TUI exit, SSH loss and process termination.
	_, _ = r.Runner.Run(ctx, nil, "systemctl", "stop", rollbackUnit+".timer", rollbackUnit+".service")
	_, _ = r.Runner.Run(ctx, nil, "systemctl", "reset-failed", rollbackUnit+".service")
	if _, e = r.Runner.Run(ctx, nil, "systemd-run", "--collect", "--unit="+rollbackUnit, "--on-active=120s", "--timer-property=AccuracySec=1s", "/usr/sbin/nft", "-f", rollbackPath); e != nil {
		return e
	}
	r.state.FirewallPending = true
	r.state.FirewallOwned = true
	r.state.RollbackAt = time.Now().Add(120 * time.Second)
	if e = saveState(statePath(r.Root), r.state); e != nil {
		return e
	}
	if _, e = r.Runner.Run(ctx, []byte(replacement), "nft", "-f", "-"); e != nil {
		return e
	}
	return nil
}

func (r *Real) Confirm(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Ready {
		return nil
	}
	if !r.state.FirewallPending || time.Until(r.state.RollbackAt) < 10*time.Second {
		return errors.New("firewall confirmation expired; rollback is automatic, run installation again")
	}
	active, e := r.Runner.Run(ctx, nil, "systemctl", "is-active", rollbackUnit+".timer")
	if e != nil || strings.TrimSpace(string(active)) != "active" {
		return errors.New("rollback timer is no longer active")
	}
	if _, e = r.Runner.Run(ctx, nil, "nft", "list", "table", "inet", firewallTable); e != nil {
		return e
	}
	// Persist the policy using a dedicated service; do not edit /etc/nftables.conf.
	script := fmt.Sprintf("#!/bin/sh\nset -eu\nif /usr/sbin/nft list table inet %s >/dev/null 2>&1; then\n { echo 'delete table inet %s'; cat '%s/firewall-active.nft'; } | /usr/sbin/nft -f -\nelse\n /usr/sbin/nft -f '%s/firewall-active.nft'\nfi\n", firewallTable, firewallTable, r.Root, r.Root)
	if e = writeFile(filepath.Join(r.Root, "firewall-apply.sh"), []byte(script), 0700); e != nil {
		return e
	}
	unit := fmt.Sprintf("[Unit]\nDescription=RemnaManager firewall\nAfter=network-pre.target\nBefore=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s/firewall-apply.sh\nExecReload=%s/firewall-apply.sh\n[Install]\nWantedBy=multi-user.target\n", r.Root, r.Root)
	if e = writeFile(r.systemPath("/etc/systemd/system/remnamanager-firewall.service"), []byte(unit), 0644); e != nil {
		return e
	}
	if _, e = r.Runner.Run(ctx, nil, "systemctl", "daemon-reload"); e != nil {
		return e
	}
	if _, e = r.Runner.Run(ctx, nil, "systemctl", "enable", "remnamanager-firewall.service"); e != nil {
		_, _ = r.Runner.Run(context.Background(), nil, "systemctl", "disable", "remnamanager-firewall.service")
		return e
	}
	if _, e = r.Runner.Run(ctx, nil, "systemctl", "stop", rollbackUnit+".timer"); e != nil {
		_, _ = r.Runner.Run(context.Background(), nil, "systemctl", "disable", "remnamanager-firewall.service")
		return e
	}
	r.state.FirewallPending = false
	r.state.Ready = true
	if e = saveState(statePath(r.Root), r.state); e != nil {
		r.state.FirewallPending = false
		r.state.Ready = false
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = r.Runner.Run(rollbackCtx, nil, "systemctl", "disable", "remnamanager-firewall.service")
		_, _ = r.Runner.Run(rollbackCtx, nil, "nft", "-f", filepath.Join(r.Root, "firewall-rollback.nft"))
		return e
	}
	return nil
}
