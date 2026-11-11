//go:build linux

package installer

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
)

func TestWriteNodeAgentFirewall(t *testing.T) {
	path, source := os.Getenv("RM_NODE_AGENT_FIREWALL_FIXTURE"), os.Getenv("RM_FIREWALL_PANEL_IP")
	if path == "" {
		t.Skip("opt-in isolated nftables namespace")
	}
	ip := net.ParseIP(source)
	if ip == nil {
		t.Fatal("invalid fixture source IP")
	}
	start, end := strings.Index(string(nodeAgent), "rules() {"), strings.Index(string(nodeAgent), "apply_file() {")
	if start < 0 || end <= start {
		t.Fatal("agent firewall function missing")
	}
	script := "set -eu\ntable=remnamanager_node\napi_port=2222\npanel_ip='" + ip.String() + "'\nssh_ports() { echo 2200; }\n" + string(nodeAgent[start:end]) + "\nrules true\n"
	runner := ExecRunner{}
	rules, err := runner.Run(context.Background(), []byte(script), "bash", "-s")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, rules, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Run(context.Background(), nil, "nft", "-c", "-f", path); err != nil {
		t.Fatal("actual agent nftables syntax failed", err)
	}
}
