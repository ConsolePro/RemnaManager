package services

import (
	"encoding/json"
	"testing"
)

func TestNodeReadsCurrentAndLegacyPanelContracts(t *testing.T) {
	for _, payload := range []string{
		`{"versions":{"xray":"26.11.9","node":"2.7.0"},"xrayUptime":3600.5,"system":{"stats":{"uptime":90000.25}}}`,
		`{"xrayVersion":"26.11.9","nodeVersion":"2.7.0","xrayUptime":"3600.5","system":{"stats":{"uptime":90000.25}}}`,
		`{"versions":{"xray":"26.11.9","node":"2.7.0"},"xrayVersion":"old","nodeVersion":"old","xrayUptime":3600.5,"system":{"stats":{"uptime":90000.25}}}`,
	} {
		var n Node
		if err := json.Unmarshal([]byte(payload), &n); err != nil {
			t.Fatal(err)
		}
		if n.CoreVersion() != "26.11.9" || n.AgentVersion() != "2.7.0" || n.XrayUptime == nil || n.System == nil || n.System.Stats.Uptime == nil {
			t.Fatal("incomplete panel data", n)
		}
	}
	var empty Node
	if err := json.Unmarshal([]byte(`{"versions":null,"system":null,"xrayUptime":null}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.CoreVersion() != "" || empty.XrayUptime != nil {
		t.Fatal("fabricated version or uptime")
	}
}
