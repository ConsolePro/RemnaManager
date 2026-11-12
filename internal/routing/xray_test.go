package routing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Supply a verified official binary to also exercise Xray's real config loader.
// The placeholder injection below tests the exact tags, not panel authorization.
func TestGeneratedTemplatesWithXray(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY is not set")
	}
	for _, kind := range []string{"balanced", "russia-preset", "raw", "ws", "grpc", "xhttp", "reality", "wireguard", "combined"} {
		t.Run(kind, func(t *testing.T) {
			spec := testSpec()
			if kind != "balanced" {
				spec.Pools[0].Hosts = []string{testHostA}
			}
			if kind == "russia-preset" {
				assets := os.Getenv("XRAY_LOCATION_ASSET")
				if _, err := os.Stat(filepath.Join(assets, "geosite.dat")); assets == "" || err != nil {
					t.Skip("official GeoSite/GeoIP assets are not available")
				}
				spec.Rules = []services.RoutingRule{{Target: "direct", Domains: []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru"}, IPs: []string{"geoip:ru"}}}
			}
			if kind != "balanced" && kind != "wireguard" && kind != "russia-preset" {
				network := kind
				if kind == "combined" || kind == "reality" {
					network = "raw"
				}
				query := "type=" + network + "&security=tls&sni=exit.example.com"
				if kind == "reality" {
					query = "type=raw&security=reality&sni=exit.example.com&pbk=" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)) + "&sid=1234&fp=chrome&flow=xtls-rprx-vision"
				}
				spec.Exits = append(spec.Exits, services.RoutingExit{Name: "bridge", Kind: "vless", ViaPool: "main", URL: "vless://41111111-2222-4333-8444-555555555555@exit.example.com:443?" + query})
				spec.DefaultTarget = "bridge"
			}
			if kind == "wireguard" || kind == "combined" {
				spec.Exits = append(spec.Exits, services.RoutingExit{Name: "warp", Kind: "wireguard", WireGuard: &services.RoutingWireGuard{SecretKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), Address: []string{"172.16.0.2/32"}, AllowedIPs: []string{"0.0.0.0/0", "::/0"}, Endpoint: "162.159.192.1:2408", MTU: 1280}})
				spec.Rules = append(spec.Rules, services.RoutingRule{Target: "warp", Domains: []string{"domain:example.net"}})
			}
			data, err := Render(spec, []byte(baseTemplate))
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			_ = json.Unmarshal(data, &config)
			var injected []any
			for _, pool := range spec.Pools {
				for i := range pool.Hosts {
					tag := "rm-" + pool.Name + "."
					if i > 0 {
						tag += "-" + strconv.Itoa(i+1)
					}
					injected = append(injected, map[string]any{"tag": tag, "protocol": "freedom"})
				}
			}
			config["outbounds"] = append(injected, config["outbounds"].([]any)...)
			delete(config, "remnawave")
			data, _ = json.Marshal(config)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "run", "-test", "-format", "json", "-config", path)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Xray rejected %s: %v\n%s", kind, err, output)
			}
		})
	}
}
