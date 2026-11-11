package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestWriteLiveNodeFixture(t *testing.T) {
	root, project := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_NODE_PROJECT")
	if root == "" || !strings.HasPrefix(project, "rmgo-nodes-test-") {
		t.Skip("opt-in disposable node fixture")
	}
	path := filepath.Join(root, "compose.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["services"].(map[string]any)["remnanode"] = map[string]any{"image": "remnawave/node:latest", "networks": []string{"panel"}, "profiles": []string{"node-test"}, "environment": map[string]string{"NODE_PORT": "2222", "SECRET_KEY": "pending"}}
	if os.Getenv("RM_NODE_V2_INTEGRATION") == "1" {
		svc := doc["services"].(map[string]any)["remnanode"].(map[string]any)
		svc["environment"] = map[string]string{"NODE_PORT": "3333", "SECRET_KEY": "pending"}
		svc["networks"] = map[string]any{"panel": map[string]any{"aliases": []string{"node.fixture.test"}}}
		svc["ports"] = []string{"127.0.0.1::80", "127.0.0.1::443"}
		conf := `server { listen 80; server_name cover.fixture.test; location / { return 301 https://$host$request_uri; } }
server { listen 127.0.0.1:8443 ssl proxy_protocol; server_name cover.fixture.test; ssl_certificate /certs/fullchain.pem; ssl_certificate_key /certs/privkey.pem; root /usr/share/nginx/html; location / { try_files $uri $uri/ =404; } }`
		if err := writeFile(filepath.Join(root, "node-nginx.conf"), []byte(conf), 0644); err != nil {
			t.Fatal(err)
		}
		entries, err := nodeAssets.ReadDir("node_assets/sites/atlas")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := nodeAssets.ReadFile("node_assets/sites/atlas/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := writeFile(filepath.Join(root, "node-site", entry.Name()), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
		doc["services"].(map[string]any)["node-site"] = map[string]any{"image": "nginx:1.28-alpine", "network_mode": "service:remnanode", "profiles": []string{"node-test"}, "volumes": []string{"./node-nginx.conf:/etc/nginx/conf.d/default.conf:ro", "./node-site:/usr/share/nginx/html:ro", "./certs/panel.test:/certs:ro"}}
	}
	data, _ = json.MarshalIndent(doc, "", "  ")
	if err = writeFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveNodeManagement(t *testing.T) {
	root, base, project := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_PANEL_API"), os.Getenv("RM_NODE_PROJECT")
	if root == "" || base == "" || !strings.HasPrefix(project, "rmgo-nodes-test-") {
		t.Skip("opt-in disposable node management")
	}
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.APIBase = base
	r.Runner = dockerProjectRunner{project: project, stack: root, real: r}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	port, err := r.Runner.Run(ctx, nil, "docker", r.composeArgs("port", "remnawave", "3000")...)
	if err != nil || base != "http://"+strings.TrimSpace(string(port))+"/api" {
		t.Fatal("API endpoint does not match the disposable Docker project")
	}
	r.state, err = loadState(statePath(r.Root))
	if err != nil {
		t.Fatal(err)
	}
	r.lock, err = os.CreateTemp(r.Root, "node-test-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.state.Ready = true
	if err = saveState(statePath(r.Root), r.state); err != nil {
		t.Fatal(err)
	}
	if err = r.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	keygen, err := r.nodeAPI(ctx, "GET", "/keygen", nil)
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		SecretKey string `json:"secretKey"`
	}
	if err = json.Unmarshal(keygen, &keys); err != nil || keys.SecretKey == "" {
		t.Fatal("node keygen failed")
	}
	path := filepath.Join(root, "compose.json")
	data, _ := os.ReadFile(path)
	var doc map[string]any
	json.Unmarshal(data, &doc)
	doc["services"].(map[string]any)["remnanode"].(map[string]any)["environment"] = map[string]string{"NODE_PORT": "2222", "SECRET_KEY": keys.SecretKey}
	data, _ = json.MarshalIndent(doc, "", "  ")
	if err = writeFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.compose(ctx, "--profile", "node-test", "up", "-d", "remnanode"); err != nil {
		t.Fatal(err)
	}
	id, err := r.compose(ctx, "--profile", "node-test", "ps", "-q", "remnanode")
	if err != nil {
		t.Fatal(err)
	}
	address, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", strings.TrimSpace(string(id)))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"log": map[string]string{"loglevel": "warning"}, "inbounds": []any{map[string]any{"tag": "RM_GO_TEST", "listen": "0.0.0.0", "port": 2443, "protocol": "vless", "settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]string{"network": "raw", "security": "none"}}}, "outbounds": []any{map[string]string{"tag": "DIRECT", "protocol": "freedom"}}}
	profile, err := r.nodeAPI(ctx, "POST", "/config-profiles", map[string]any{"name": "Go_Test_" + time.Now().Format("150405"), "config": config})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		UUID string `json:"uuid"`
	}
	if err = json.Unmarshal(profile, &created); err != nil || created.UUID == "" {
		t.Fatal("config profile was not created")
	}
	inboundData, err := r.nodeAPI(ctx, "GET", "/config-profiles/"+created.UUID+"/inbounds", nil)
	if err != nil {
		t.Fatal(err)
	}
	var inbounds struct {
		Inbounds []struct {
			UUID string `json:"uuid"`
		} `json:"inbounds"`
	}
	if err = json.Unmarshal(inboundData, &inbounds); err != nil || len(inbounds.Inbounds) != 1 {
		t.Fatal("test inbound missing")
	}
	nodeData, err := r.nodeAPI(ctx, "POST", "/nodes", map[string]any{"name": "Go_Node_" + time.Now().Format("150405"), "address": strings.TrimSpace(string(address)), "port": 2222, "configProfile": map[string]any{"activeConfigProfileUuid": created.UUID, "activeInbounds": []string{inbounds.Inbounds[0].UUID}}})
	if err != nil {
		t.Fatal(err)
	}
	var node services.Node
	if err = json.Unmarshal(nodeData, &node); err != nil || node.UUID == "" {
		t.Fatal("node was not created")
	}
	connected := false
	for attempts := 0; attempts < 50; attempts++ {
		raw, e := r.nodeAPI(ctx, "GET", "/nodes/"+node.UUID, nil)
		if e != nil {
			t.Fatal(e)
		}
		json.Unmarshal(raw, &node)
		if node.IsConnected {
			connected = true
			break
		}
		time.Sleep(time.Second)
	}
	if !connected {
		t.Fatal("test node did not connect to its isolated panel")
	}
	run := func(action, value string) services.NodeResult {
		t.Helper()
		t.Log("node operation:", action)
		result, e := r.manageNodes(ctx, services.NodeRequest{Action: action, UUID: node.UUID, Value: value})
		if e != nil {
			t.Fatalf("%s: %v", action, e)
		}
		return result
	}
	list := run("nodes-list", "")
	if len(list.Nodes) != 1 || !list.Nodes[0].IsConnected {
		t.Fatal("real connected node missing")
	}
	status := run("node-status", "")
	if !strings.Contains(status.Text, node.UUID) {
		t.Fatal("node detail missing")
	}
	run("node-rename", "Go_Renamed_Node")
	run("node-restart", "")
	run("node-toggle", "disable")
	run("node-toggle", "disable")
	run("node-toggle", "enable")
	list = run("nodes-list", "")
	if len(list.Nodes) != 1 || list.Nodes[0].Name != "Go_Renamed_Node" || list.Nodes[0].IsDisabled {
		t.Fatal("node state was not updated")
	}
	// Deletion is only test cleanup; the application has no node deletion action.
	if _, err = r.nodeAPI(ctx, "DELETE", "/nodes/"+node.UUID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = r.nodeAPI(ctx, "DELETE", "/config-profiles/"+created.UUID, nil); err != nil {
		t.Fatal(err)
	}
}
