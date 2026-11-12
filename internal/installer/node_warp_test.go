package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

type warpTestState struct {
	config                               json.RawMessage
	patches, installs, restarts, removes int
	shared, losePatch, failRemove        bool
	failStage                            string
}

func warpFixture(t *testing.T) (*Real, NodeJob, services.Node, *warpTestState) {
	t.Helper()
	r, runner, j := updateFixture(t)
	j.Version = 4
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	j.ProfileUUID = routeBaseID
	j.InboundUUID = routeHostID
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	node := services.Node{UUID: j.NodeUUID, Name: j.Params.Name, Address: j.Params.Address, IsConnected: true}
	f := &warpTestState{config: json.RawMessage(`{"inbounds":[{"tag":"entry","protocol":"socks","listen":"127.0.0.1","port":10808}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","network":"tcp,udp","outboundTag":"direct"}]}}`)}
	r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		var response any
		if req.URL.Host == "api.github.com" {
			if req.URL.Path != "/repos/ViRb3/wgcf/releases/latest" {
				t.Fatal("unexpected download", req.URL)
			}
			assets := []map[string]any{}
			for _, arch := range []string{"amd64", "arm64"} {
				name := "wgcf_2.3.1_linux_" + arch
				assets = append(assets, map[string]any{"name": name, "browser_download_url": "https://github.com/ViRb3/wgcf/releases/download/v2.3.1/" + name, "digest": "sha256:" + strings.Repeat("a", 64)})
			}
			data, _ := json.Marshal(map[string]any{"tag_name": "v2.3.1", "assets": assets})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
		}
		switch {
		case req.Method == "GET" && req.URL.Path == "/config-profiles/"+j.ProfileUUID:
			response = map[string]any{"uuid": j.ProfileUUID, "config": f.config, "inbounds": []map[string]string{{"uuid": j.InboundUUID, "tag": "entry"}}}
		case req.Method == "GET" && req.URL.Path == "/nodes":
			nodes := []any{node}
			if f.shared {
				nodes = append(nodes, map[string]any{"uuid": routeNodeID, "configProfile": map[string]any{"activeConfigProfileUuid": j.ProfileUUID}})
			}
			response = map[string]any{"nodes": nodes}
		case req.Method == "GET" && req.URL.Path == "/nodes/"+j.NodeUUID:
			response = node
		case req.Method == "POST" && req.URL.Path == "/nodes/"+j.NodeUUID+"/actions/restart":
			f.restarts++
			response = map[string]any{}
		case req.Method == "PATCH" && req.URL.Path == "/config-profiles":
			var body struct {
				UUID   string          `json:"uuid"`
				Config json.RawMessage `json:"config"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.UUID != j.ProfileUUID {
				t.Fatal("wrong profile modified")
			}
			f.config = body.Config
			f.patches++
			if f.losePatch {
				f.losePatch = false
				return nil, errors.New("lost PATCH acknowledgement")
			}
			response = map[string]any{}
		default:
			t.Fatal("unexpected API mutation", req.Method, req.URL.Path)
		}
		data, _ := json.Marshal(map[string]any{"response": response})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})
	runner.response = func(call string, in []byte) ([]byte, error) {
		if strings.Contains(call, j.Secret) || strings.Contains(call, j.RealityKey) {
			t.Fatal("secret in SSH arguments")
		}
		payload := string(in)
		for _, action := range []string{"install", "remove", "validate", "checkpoint"} {
			if !strings.Contains(payload, "RM_WARP_ACTION='"+action+"'") {
				continue
			}
			if action == "install" {
				f.installs++
				if f.failStage == "trace" {
					return []byte("RM_WARP_FAILURE=trace\n"), errors.New("probe failed")
				}
			}
			if action == "validate" && f.failStage == "validate" {
				return []byte("RM_WARP_FAILURE=validate\n"), errors.New("config rejected")
			}
			if action == "remove" {
				f.removes++
				if f.failRemove {
					f.failRemove = false
					return nil, errors.New("SSH lost")
				}
			}
			return []byte("RM_WARP_OK=" + action + "\n"), nil
		}
		t.Fatal("unexpected remote command", call)
		return nil, nil
	}
	return r, j, node, f
}

func TestNativeWarpInstallResumeAndRemoveRetainUnrelatedProfileEdits(t *testing.T) {
	r, j, node, f := warpFixture(t)
	before := jsonHash(f.config)
	f.losePatch = true
	if _, err := r.manageNodeWarp(context.Background(), services.NodeRequest{Action: "node-warp-install-run"}, node); err == nil {
		t.Fatal("lost response hidden")
	}
	if _, err := r.manageNodeWarp(context.Background(), services.NodeRequest{Action: "node-warp-install-run"}, node); err != nil {
		t.Fatal(err)
	}
	if f.patches != 1 || f.restarts != 1 {
		t.Fatal("duplicate profile patch or missing confirmed restart", f.patches, f.restarts)
	}
	saved, err := r.installedNodeJob(j.NodeUUID)
	if err != nil || !saved.WarpEnabled {
		t.Fatal("node updates will lose WARP capabilities", err)
	}
	var profile map[string]any
	_ = json.Unmarshal(f.config, &profile)
	profile["dns"] = map[string]any{"servers": []string{"9.9.9.9"}}
	profile["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)["domain"] = []string{"domain:example.org"}
	f.config = mustJSON(profile)
	f.failRemove = true
	if _, err := r.manageNodeWarp(context.Background(), services.NodeRequest{Action: "node-warp-remove-run"}, node); err == nil {
		t.Fatal("lost remote removal hidden")
	}
	if _, err := r.manageNodeWarp(context.Background(), services.NodeRequest{Action: "node-warp-remove-run"}, node); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f.config), nativeWarpTag) || !strings.Contains(string(f.config), "9.9.9.9") || before == jsonHash(f.config) {
		t.Fatal("unrelated edits lost or WARP rules retained", string(f.config))
	}
	if f.patches != 2 {
		t.Fatal("removal patched profile twice", f.patches)
	}
	saved, err = r.installedNodeJob(j.NodeUUID)
	if err != nil || saved.WarpEnabled {
		t.Fatal("WARP flag remained after removal", err)
	}
}

func TestNativeWarpFailureCannotChangePanelProfile(t *testing.T) {
	for _, stage := range []string{"trace", "validate", "shared-profile"} {
		t.Run(stage, func(t *testing.T) {
			r, _, node, f := warpFixture(t)
			before := jsonHash(f.config)
			f.failStage = stage
			f.shared = stage == "shared-profile"
			if _, err := r.manageNodeWarp(context.Background(), services.NodeRequest{Action: "node-warp-install-run"}, node); err == nil {
				t.Fatal("failure hidden")
			}
			if f.patches != 0 || jsonHash(f.config) != before {
				t.Fatal("profile changed before remote checks succeeded")
			}
			if f.shared && f.installs != 0 {
				t.Fatal("WARP installed despite shared profile")
			}
		})
	}
}

func TestNativeWarpCapabilitiesSurviveNodeComposeRegeneration(t *testing.T) {
	j := provisionJob(t)
	j.Image = "remnawave/node@sha256:" + strings.Repeat("a", 64)
	j.WarpEnabled = true
	data, err := RenderNodeStack(j)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "NET_ADMIN") || !strings.Contains(string(data), "NET_RAW") {
		t.Fatal("node update removed native WARP capabilities")
	}
}

func TestNativeWarpProfileWithXray(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY is not set")
	}
	before := json.RawMessage(`{"inbounds":[{"tag":"entry","protocol":"socks","listen":"127.0.0.1","port":10808}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","network":"tcp,udp","outboundTag":"direct"}]}}`)
	after, err := nativeWarpConfig(before, "entry")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "warp.json")
	if err := os.WriteFile(path, after, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "run", "-test", "-format", "json", "-config", path).CombinedOutput(); err != nil {
		t.Fatalf("Xray rejected native WARP configuration: %v\n%s", err, output)
	}
}
