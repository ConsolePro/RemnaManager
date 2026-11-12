package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/updater"
)

func TestXrayUpdateRecoveryAndImagePreservation(t *testing.T) {
	for _, scenario := range []string{"success", "apply-fails", "commit-response-lost", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			r, _, j := updateFixture(t)
			j.Version = 4
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
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
					data, _ = json.Marshal(map[string]any{"response": map[string]any{"uuid": j.NodeUUID, "isConnected": true, "isDisabled": false, "xrayVersion": strings.TrimPrefix(target, "v")}})
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
			if scenario == "success" && err != nil {
				t.Fatal(err)
			}
			if scenario != "success" && err == nil {
				t.Fatal("failure was hidden")
			}
			committed := scenario == "success" || scenario == "commit-response-lost"
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
