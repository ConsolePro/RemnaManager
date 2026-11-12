package routing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

const baseTemplate = `{"log":{"loglevel":"warning"},"dns":{"servers":["1.1.1.1"]},"inbounds":[{"tag":"socks","listen":"127.0.0.1","port":10808,"protocol":"socks","settings":{"udp":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}],"policy":{"levels":{"0":{"handshake":4}}},"routing":{"rules":[{"ip":["10.0.0.0/8"],"outboundTag":"block"},{"network":"tcp,udp","outboundTag":"proxy"}]}}`
const testBaseID = "11111111-2222-4333-8444-555555555555"
const testHostA = "21111111-2222-4333-8444-555555555555"
const testHostB = "31111111-2222-4333-8444-555555555555"

func testSpec() services.RoutingSpec {
	return services.RoutingSpec{Name: "Client_route", BaseTemplateUUID: testBaseID, DefaultTarget: "main", Pools: []services.RoutingPool{{Name: "main", Hosts: []string{testHostA, testHostB}}}, Rules: []services.RoutingRule{{Target: "direct", Domains: []string{"domain:ru"}, IPs: []string{"192.0.2.0/24"}}}}
}

func TestClientSplitAndBalancePreserveBaseAndFailClosed(t *testing.T) {
	spec := testSpec()
	base := []byte(baseTemplate)
	before := append([]byte(nil), base...)
	data, err := Render(spec, base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(base, before) {
		t.Fatal("base was modified")
	}
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if !reflect.DeepEqual(result["policy"], map[string]any{"levels": map[string]any{"0": map[string]any{"handshake": float64(4)}}}) {
		t.Fatal("custom base settings were lost")
	}
	groups := result["remnawave"].(map[string]any)["injectHosts"].([]any)
	group := groups[0].(map[string]any)
	if group["selectFrom"] != "ALL" || group["tagPrefix"] != "rm-main." {
		t.Fatal("source visibility was changed or prefix excludes first host")
	}
	route := result["routing"].(map[string]any)
	rules := route["rules"].([]any)
	if len(rules) != 4 {
		t.Fatal("domain and IP must match independently; base catch-all must be replaced", rules)
	}
	if rules[0].(map[string]any)["outboundTag"] != "rm-direct" || rules[1].(map[string]any)["outboundTag"] != "rm-direct" || rules[2].(map[string]any)["outboundTag"] != "block" || rules[3].(map[string]any)["balancerTag"] != "rm-main" {
		t.Fatal("wrong routing priority", rules)
	}
	balancer := route["balancers"].([]any)[0].(map[string]any)
	if balancer["fallbackTag"] != "rm-block" || !strings.Contains(string(data), `"leastPing"`) {
		t.Fatal("a failed pool must not fall back to direct")
	}
	if strings.Contains(string(data), "vless://") || strings.Contains(string(data), "clients") {
		t.Fatal("template embedded user connection data")
	}
}

func TestBridgeAndWarpShareOneTemplate(t *testing.T) {
	spec := testSpec()
	spec.Pools[0].Hosts = []string{testHostA}
	wg := &services.RoutingWireGuard{SecretKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), Endpoint: "162.159.192.1:2408", Address: []string{"172.16.0.2/32"}, AllowedIPs: []string{"0.0.0.0/0", "::/0"}, MTU: 1280}
	spec.Exits = []services.RoutingExit{{Name: "bridge", Kind: "vless", ViaPool: "main", URL: "vless://41111111-2222-4333-8444-555555555555@exit.example.com:443?security=tls&sni=exit.example.com&type=tcp"}, {Name: "warp", Kind: "wireguard", WireGuard: wg}}
	spec.Rules = append(spec.Rules, services.RoutingRule{Target: "warp", Domains: []string{"domain:example.net"}})
	spec.DefaultTarget = "bridge"
	data, err := Render(spec, []byte(baseTemplate))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	found := map[string]map[string]any{}
	for _, value := range result["outbounds"].([]any) {
		out := value.(map[string]any)
		found[out["tag"].(string)] = out
	}
	if found["rm-warp"]["protocol"] != "wireguard" || found["rm-bridge"]["protocol"] != "vless" {
		t.Fatal("missing shared outbounds")
	}
	stream := found["rm-bridge"]["streamSettings"].(map[string]any)
	if stream["sockopt"].(map[string]any)["dialerProxy"] != "rm-main." {
		t.Fatal("bridge bypasses entry")
	}
	rules := result["routing"].(map[string]any)["rules"].([]any)
	if rules[len(rules)-1].(map[string]any)["outboundTag"] != "rm-bridge" {
		t.Fatal("wrong default bridge route")
	}
	spec.Pools[0].Hosts = append(spec.Pools[0].Hosts, testHostB)
	if _, err := Render(spec, []byte(baseTemplate)); err == nil {
		t.Fatal("bridge entry cannot silently use an ambiguous pool")
	}
}

func TestWireGuardImportNeverExecutesHooks(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	data := []byte("[Interface]\nPrivateKey = " + key + "\nAddress = 172.16.0.2/32\nPostUp = this-is-not-a-command\n[Peer]\nPublicKey = " + key + "\nEndpoint = 162.159.192.1:2408\nAllowedIPs = 0.0.0.0/0\n")
	wg, err := ParseWireGuard(data)
	if err != nil || wg.SecretKey != key {
		t.Fatal(wg, err)
	}
	if len(wg.AllowedIPs) != 1 || wg.AllowedIPs[0] != "0.0.0.0/0" {
		t.Fatal("import changed peer allowed IPs", wg.AllowedIPs)
	}
	if _, err := ParseWireGuard(append(data, []byte("[Peer]\nPublicKey = other\n")...)); err == nil {
		t.Fatal("multiple peers were accepted")
	}
}

func TestInvalidGraphsDoNotRender(t *testing.T) {
	for _, kind := range []string{"missing-target", "bad-ip", "duplicate-host", "namespace", "injector", "balancer-namespace"} {
		t.Run(kind, func(t *testing.T) {
			spec := testSpec()
			base := baseTemplate
			switch kind {
			case "missing-target":
				spec.DefaultTarget = "unknown"
			case "bad-ip":
				spec.Rules[0].IPs = []string{"not-an-ip"}
			case "duplicate-host":
				spec.Pools[0].Hosts = []string{testHostA, testHostA}
			case "namespace":
				base = strings.ReplaceAll(base, `"tag":"direct"`, `"tag":"rm-main"`)
			case "injector":
				base = strings.Replace(base, `{"log":`, `{"remnawave":{"injectHosts":[]},"log":`, 1)
			case "balancer-namespace":
				base = strings.Replace(base, `"routing":{`, `"routing":{"balancers":[{"tag":"rm-main"}],`, 1)
			}
			if _, err := Render(spec, []byte(base)); err == nil {
				t.Fatal("unsafe graph accepted")
			}
		})
	}
}

func TestOversizedSchemeIsRefusedBeforeRendering(t *testing.T) {
	spec := testSpec()
	// Each rule is individually valid, but the combined recipe must remain
	// small enough to journal its config and creation snapshot safely.
	rule := services.RoutingRule{Target: "direct", Domains: []string{}}
	for i := 0; i < 128; i++ {
		rule.Domains = append(rule.Domains, "domain:"+strings.Repeat("a", 120)+".example.com")
	}
	for i := 0; i < 40; i++ {
		spec.Rules = append(spec.Rules, rule)
	}
	if _, err := Render(spec, []byte(baseTemplate)); err == nil {
		t.Fatal("oversized scheme was rendered")
	}
}
