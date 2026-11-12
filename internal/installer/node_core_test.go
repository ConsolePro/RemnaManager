package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/updater"
)

func TestCoreVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, current, action, target string
		noChange, wantError           bool
	}{
		{"older", "26.9.9", "node-core-prepare", "", false, false},
		{"equal", "v26.10.31", "node-core-prepare", "", true, false},
		{"newer", "26.11.9", "node-core-prepare", "", true, false},
		{"unknown", "", "node-core-prepare", "", false, true},
		{"invalid", "custom-build", "node-core-prepare", "", false, true},
		{"changed-after-confirmation", "26.10.31", "node-core-run", "v26.10.31", true, false},
		{"update-cannot-downgrade", "26.11.9", "node-core-run", "v26.10.31", true, false},
		{"rollback-cannot-upgrade", "26.9.9", "node-core-rollback-run", "v26.10.31", false, true},
		{"rollback-cannot-reinstall", "26.10.31", "node-core-rollback-run", "v26.10.31", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, j := updateFixture(t)
			j.Version = 4
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
			}
			if err := r.saveNodeJob(&j); err != nil {
				t.Fatal(err)
			}
			r.Runner = updateContextRunner{run: func(context.Context, []byte) ([]byte, error) {
				t.Fatal("version check must not change or restart the node")
				return nil, nil
			}}
			r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://api.github.com/repos/XTLS/Xray-core/releases/latest" {
					t.Fatal("unexpected download", req.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v26.10.31"}`)), Header: make(http.Header)}, nil
			})
			node := services.Node{UUID: j.NodeUUID, Address: j.Params.Address, IsConnected: true, XrayVersion: tc.current}
			result, err := r.manageNodeCore(context.Background(), services.NodeRequest{Action: tc.action, Value: tc.target}, node)
			if (err != nil) != tc.wantError || result.NoChange != tc.noChange {
				t.Fatal(result, err)
			}
			if tc.name == "older" && result.Version != "v26.10.31" {
				t.Fatal("missing update candidate", result)
			}
		})
	}
}

func TestCoreRollbackListsOnlyOlderStableReleases(t *testing.T) {
	r, _, j := updateFixture(t)
	j.Version = 4
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.github.com/repos/XTLS/Xray-core/releases?per_page=100" {
			t.Fatal("unexpected catalog", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[
		{"tag_name":"v26.9.9"},{"tag_name":"v26.11.9"},{"tag_name":"v26.10.31"},
		{"tag_name":"v26.10.21"},{"tag_name":"v26.7.28","prerelease":true},
		{"tag_name":"v26.7.11","draft":true},{"tag_name":"nightly"},{"tag_name":"v26.9.9"}]`)), Header: make(http.Header)}, nil
	})
	node := services.Node{UUID: j.NodeUUID, Address: j.Params.Address, IsConnected: true, XrayVersion: "26.10.31"}
	result, err := r.manageNodeCore(context.Background(), services.NodeRequest{Action: "node-core-rollback-prepare"}, node)
	if err != nil || strings.Join(result.Versions, ",") != "v26.10.21,v26.9.9" {
		t.Fatal(result, err)
	}
	node.XrayVersion = "1.0.0"
	result, err = r.manageNodeCore(context.Background(), services.NodeRequest{Action: "node-core-rollback-prepare"}, node)
	if err != nil || !result.NoChange || len(result.Versions) != 0 {
		t.Fatal(result, err)
	}
}

func TestFreshNodeUsesBundledCore(t *testing.T) {
	j := provisionJob(t)
	j.Image = "remnawave/node@sha256:" + strings.Repeat("a", 64)
	compose, err := RenderNodeStack(j)
	if err != nil {
		t.Fatal(err)
	}
	var stack struct {
		Services map[string]struct {
			Image   string
			Volumes []any
		}
	}
	if err := json.Unmarshal(compose, &stack); err != nil {
		t.Fatal(err)
	}
	node := stack.Services["remnanode"]
	if node.Image != j.Image || len(node.Volumes) != 0 {
		t.Fatal("fresh node must use the image's bundled Xray", string(compose))
	}
}

func TestXrayUpdateRecoveryAndImagePreservation(t *testing.T) {
	for _, scenario := range []string{"success", "rollback-success", "apply-fails", "commit-response-lost", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			r, _, j := updateFixture(t)
			j.Version = 4
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
			}
			if scenario == "rollback-success" {
				j.CoreVersion = "v26.10.31"
				j.CoreBinary = "/opt/remnanode/xray/v26.10.31/amd64/xray"
			}
			if err := r.saveNodeJob(&j); err != nil {
				t.Fatal(err)
			}
			const target = "v26.9.9"
			originalImage, originalKey := j.Image, j.RealityKey
			r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				var data []byte
				headers := make(http.Header)
				if req.URL.Host == "api.github.com" {
					release := updater.Release{Version: target}
					for _, name := range []string{"Xray-linux-64.zip", "Xray-linux-arm64-v8a.zip"} {
						release.Assets = append(release.Assets, updater.Asset{Name: name, URL: "https://github.com/XTLS/Xray-core/releases/download/" + target + "/" + name, Digest: "sha256:" + strings.Repeat("a", 64)})
					}
					data, _ = json.Marshal(release)
				} else if req.URL.Hostname() == j.Params.Domain {
					if req.URL.Scheme == "http" {
						headers.Set("Location", "https://"+j.Params.Domain+"/")
						return &http.Response{StatusCode: 301, Body: io.NopCloser(strings.NewReader("")), Header: headers}, nil
					}
					data = []byte("<html>website</html>")
				} else {
					data, _ = json.Marshal(map[string]any{"response": map[string]any{"uuid": j.NodeUUID, "isConnected": true, "isDisabled": false, "versions": map[string]string{"xray": strings.TrimPrefix(target, "v"), "node": "2.7.0"}}})
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: headers}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			phase := "missing"
			r.Runner = updateContextRunner{run: func(callCtx context.Context, input []byte) ([]byte, error) {
				if callCtx.Err() != nil {
					t.Fatal("cancelled context reused for recovery")
				}
				script := string(input)
				if strings.Contains(script, "docker pull") {
					t.Fatal("core update must preserve node image")
				}
				switch {
				case strings.Contains(script, "RM_CORE_ACTION='prepare'"):
					phase = "prepared"
				case strings.Contains(script, "RM_CORE_ACTION='apply'"):
					phase = "trial"
					if scenario == "apply-fails" {
						return nil, errors.New("lost SSH")
					}
					if scenario == "cancelled" {
						cancel()
						return nil, context.Canceled
					}
				case strings.Contains(script, "RM_CORE_ACTION='commit'"):
					phase = "committed"
					if scenario == "commit-response-lost" {
						return nil, errors.New("lost commit acknowledgement")
					}
				case strings.Contains(script, "RM_CORE_ACTION='rollback'"):
					phase = "rolled-back"
				}
				return []byte("RM_CORE_PHASE=" + phase + "\nRM_CORE_VERSION=" + target + "\nRM_CORE_PATH=/opt/remnanode/xray/" + target + "/amd64/xray\n"), nil
			}}
			err := r.updateNodeCore(ctx, &j, target)
			if (scenario == "success" || scenario == "rollback-success") && err != nil {
				t.Fatal(err)
			}
			if scenario != "success" && scenario != "rollback-success" && err == nil {
				t.Fatal("failure was hidden")
			}
			committed := scenario == "success" || scenario == "rollback-success" || scenario == "commit-response-lost"
			if committed && (j.CoreVersion != target || j.CoreBinary == "") {
				t.Fatal("committed core was not reconciled")
			}
			if !committed && (j.CoreVersion != "" || phase != "rolled-back") {
				t.Fatal("failed trial was not rolled back")
			}
			if j.CoreUpdateID != "" || j.Image != originalImage || j.RealityKey != originalKey {
				t.Fatal("core update changed node image, credentials or left a transaction")
			}
			loaded, err := r.loadNodeJob(j.ID)
			if err != nil || loaded.CoreBinary != j.CoreBinary {
				t.Fatal("core journal did not survive reload", err)
			}
			compose, err := RenderNodeStack(j)
			if err != nil {
				t.Fatal(err)
			}
			if committed && !strings.Contains(string(compose), j.CoreBinary) {
				t.Fatal("subsequent rendering dropped the core mount")
			}
		})
	}
}

func TestXrayMissingRemotePreparationClearsOnlyCoreTransaction(t *testing.T) {
	r, runner, j := updateFixture(t)
	j.CoreUpdateID = strings.Repeat("b", 32)
	j.CoreTargetVersion = "v26.9.9"
	runner.response = func(_ string, _ []byte) ([]byte, error) { return []byte("RM_CORE_PHASE=missing\n"), nil }
	if err := r.recoverNodeCore(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if j.CoreUpdateID != "" || j.CoreTargetVersion != "" || j.Image == "" {
		t.Fatal("missing preparation damaged node state")
	}
}
