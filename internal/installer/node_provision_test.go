package installer

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func provisionJob(t *testing.T) NodeJob {
	t.Helper()
	curve, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	wire = append(wire, 0, 0, 0, 32)
	wire = append(wire, pub...)
	return NodeJob{Version: 1, ID: strings.Repeat("a", 32), Params: services.NodeInstallParameters{Name: "Test_Node", Address: "192.0.2.10", Domain: "cover.example.test", PanelIP: "192.0.2.20", SSH: services.NodeSSH{Host: "node.example.test", Port: 22, HostKey: "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire)}}, Email: "admin@example.test", Secret: "private-node-secret-123", RealityKey: base64.RawURLEncoding.EncodeToString(curve.Bytes()), ShortID: "0123456789abcdef", Completed: map[int]bool{}, CreatedAt: time.Now().UTC()}
}

func TestNodeJournalPreservesKeysAndPublicStatusHidesSecrets(t *testing.T) {
	r, _ := managementFixture(t)
	j := provisionJob(t)
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.loadNodeJob(j.ID)
	if err != nil || loaded.RealityKey != j.RealityKey || loaded.Secret != j.Secret {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(jobStatus(loaded))
	for _, secret := range []string{j.Secret, j.RealityKey, j.Params.SSH.HostKey} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret in public installation status")
		}
	}
	jobs, err := r.pendingNodeJobs()
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	j.Completed[8] = true
	j.Ready = true
	if err = r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	jobs, err = r.pendingNodeJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatal("completed installation is still pending", err)
	}
	j.RealityKey = "corrupted"
	r.saveNodeJob(&j)
	if _, err = r.loadNodeJob(j.ID); err == nil {
		t.Fatal("corrupt keys were silently regenerated")
	}
	if _, err = r.loadNodeJob("../../panel"); err == nil {
		t.Fatal("unsafe journal path accepted")
	}
}

func TestInterruptedProfileAndHostCreationReconcilesWithoutDuplicates(t *testing.T) {
	r, _ := managementFixture(t)
	j := provisionJob(t)
	const profileID = "11111111-2222-4333-8444-000000000001"
	const inboundID = "11111111-2222-4333-8444-000000000002"
	const nodeID = "11111111-2222-4333-8444-000000000003"
	const hostID = "11111111-2222-4333-8444-000000000004"
	const squadID = "11111111-2222-4333-8444-000000000005"
	collections := map[string][]map[string]any{"/config-profiles": {}, "/nodes": {}, "/hosts": {}, "/internal-squads": {}}
	posts := map[string]int{}
	nodeTestAPI(t, r, func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/api")
		if req.Method == "GET" {
			if path == "/config-profiles/"+profileID {
				json.NewEncoder(w).Encode(map[string]any{"response": collections["/config-profiles"][0]})
				return
			}
			if path == "/config-profiles/"+profileID+"/inbounds" {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"inbounds": []any{map[string]string{"tag": "RM_REALITY_" + j.ID[:12], "uuid": inboundID}}}})
				return
			}
			if values, ok := collections[path]; ok {
				json.NewEncoder(w).Encode(map[string]any{"response": values})
				return
			}
		}
		if req.Method == "POST" {
			var body map[string]any
			json.NewDecoder(req.Body).Decode(&body)
			ids := map[string]string{"/config-profiles": profileID, "/nodes": nodeID, "/hosts": hostID, "/internal-squads": squadID}
			id, ok := ids[path]
			if !ok {
				t.Error(path)
				w.WriteHeader(404)
				return
			}
			body["uuid"] = id
			collections[path] = append(collections[path], body)
			posts[path]++
			// Simulate a committed POST whose response was lost during SSH/API loss.
			if path == "/config-profiles" || path == "/hosts" {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": body})
			return
		}
		t.Error(req.Method, path)
		w.WriteHeader(404)
	})
	if err := r.ensureNodeProfile(context.Background(), &j); err == nil {
		t.Fatal("lost response unexpectedly succeeded")
	}
	if err := r.ensureNodeProfile(context.Background(), &j); err != nil {
		t.Fatal("profile was not recovered", err)
	}
	if err := r.ensureNodeObjects(context.Background(), &j); err == nil {
		t.Fatal("lost host response unexpectedly succeeded")
	}
	if err := r.ensureNodeObjects(context.Background(), &j); err != nil {
		t.Fatal("host was not recovered", err)
	}
	if err := r.ensureNodeProfile(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureNodeObjects(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/config-profiles", "/nodes", "/hosts", "/internal-squads"} {
		if posts[path] != 1 {
			t.Fatal("duplicate resource", path, posts[path])
		}
	}
	before := j.RealityKey
	collections["/config-profiles"][0]["config"] = map[string]any{"changed": true}
	if err := r.ensureNodeProfile(context.Background(), &j); err == nil || j.RealityKey != before {
		t.Fatal("overwrote an externally changed profile")
	}
	collections["/nodes"] = nil
	if err := r.ensureNodeObjects(context.Background(), &j); err == nil {
		t.Fatal("replaced an externally deleted node")
	}
}

func TestNodeStageFailureAndConfirmationBoundaries(t *testing.T) {
	r, f := managementFixture(t)
	j := provisionJob(t)
	f.response = func(call string, _ []byte) ([]byte, error) { return []byte("Node stage complete\n"), nil }
	if err := r.runNodeStage(context.Background(), &j, 3); err == nil {
		t.Fatal("skipped dependencies")
	}
	if err := r.confirmNodeInstall(context.Background(), &j); err == nil {
		t.Fatal("committed firewall without trial")
	}
	if err := r.runNodeStage(context.Background(), &j, 0); err != nil {
		t.Fatal(err)
	}
	if j.FirewallPending || j.Completed[7] || j.Completed[8] {
		t.Fatal("preflight incorrectly confirmed firewall")
	}
	data, err := RenderNodeStack(j)
	if err != nil {
		t.Fatal(err)
	}
	var stack struct {
		Services map[string]struct {
			NetworkMode string `json:"network_mode"`
			Ports       []any
			Environment map[string]string
		}
	}
	json.Unmarshal(data, &stack)
	if stack.Services["remnanode"].NetworkMode != "host" || len(stack.Services["remnanode"].Ports) != 0 || stack.Services["remnanode"].Environment["SECRET_KEY"] != j.Secret {
		t.Fatal("invalid node stack")
	}
	if err = r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	backup, err := r.backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(backup, "node-jobs", j.ID+".json")); err != nil {
		t.Fatal("node journals were omitted from backup")
	}
}

func TestNodeAgentPayloadHasNoSecretsInArguments(t *testing.T) {
	r, f := managementFixture(t)
	j := provisionJob(t)
	seen := false
	f.response = func(call string, in []byte) ([]byte, error) {
		if strings.Contains(call, j.Secret) || strings.Contains(call, j.RealityKey) {
			t.Fatal("secret leaked into process arguments")
		}
		if !strings.Contains(string(in), "RM_COMPOSE_B64=") || !strings.Contains(call, "BatchMode=yes") || !strings.Contains(call, "StrictHostKeyChecking=yes") {
			t.Fatal("unsafe SSH payload")
		}
		seen = true
		return nil, nil
	}
	if _, err := r.runNodeAgent(context.Background(), j, 3); err != nil || !seen {
		t.Fatal(err)
	}
	f.response = func(string, []byte) ([]byte, error) {
		return []byte("\x1b[31mfailed " + j.Secret + " " + j.RealityKey + "\x1b[0m"), errors.New("remote command failed")
	}
	if _, err := r.runNodeAgent(context.Background(), j, 3); err == nil || strings.Contains(err.Error(), j.Secret) || strings.Contains(err.Error(), j.RealityKey) || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatal("remote diagnostics leaked secrets or terminal escapes")
	}
	for _, p := range []services.NodeInstallParameters{
		{Name: "Good", Address: "node.test", Domain: "cover.test", PanelIP: "127.0.0.1", SSH: j.Params.SSH},
		{Name: "Good", Address: "node.test;id", Domain: "cover.test", PanelIP: "192.0.2.20", SSH: j.Params.SSH},
	} {
		if ValidateNodeInstall(p) == nil {
			t.Fatal("unsafe install parameters accepted")
		}
	}
}

func TestPanelIPDetectionAndConfirmation(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{"banner\nRM_SSH_CONNECTION=192.0.2.20 54321 198.51.100.10 22\n", "192.0.2.20"},
		{"RM_SSH_CONNECTION=2001:db8::20 54321 2001:db8::10 22\n", "2001:db8::20"},
		{"RM_SSH_CONNECTION=::ffff:192.0.2.20 54321 198.51.100.10 22\n", "192.0.2.20"},
		{"RM_SSH_CONNECTION=10.0.0.2 54321 10.0.0.3 22\n", "10.0.0.2"},
		{"RM_SSH_CONNECTION=127.0.0.1 54321 127.0.0.1 22\n", ""},
		{"RM_SSH_CONNECTION=fe80::1 54321 fe80::2 22\n", ""},
		{"RM_SSH_CONNECTION=garbage\n", ""},
		{"RM_SSH_CONNECTION=192.0.2.20 1 192.0.2.10 22\nRM_SSH_CONNECTION=192.0.2.21 1 192.0.2.10 22\n", ""},
		{"", ""},
	} {
		got, err := panelIPFromSSH([]byte(tc.data))
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("parse %q: got %q, error %v", tc.data, got, err)
		}
	}
	r, f := managementFixture(t)
	j := provisionJob(t)
	j.Params.PanelIP = ""
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	f.response = func(call string, in []byte) ([]byte, error) {
		if !strings.Contains(call, "BatchMode=yes") || !strings.Contains(string(in), "$SSH_CONNECTION") {
			t.Fatal("detection did not use the pinned SSH session")
		}
		return []byte("RM_SSH_CONNECTION=192.0.2.20 12345 198.51.100.10 22\n"), nil
	}
	result, err := r.nodeProvision(context.Background(), services.NodeRequest{Action: "node-install-detect-panel-ip", JobID: j.ID})
	if err != nil || result.Text != "192.0.2.20" {
		t.Fatal(result.Text, err)
	}
	saved, err := r.loadNodeJob(j.ID)
	if err != nil || saved.Params.PanelIP != "" {
		t.Fatal("detection must not persist an unconfirmed address", err)
	}
	if err := r.runNodeStage(context.Background(), &saved, 0); err == nil {
		t.Fatal("installation started without a confirmed address")
	}
	request := services.NodeRequest{Action: "node-install-panel-ip", JobID: j.ID, Install: services.NodeInstallParameters{PanelIP: "192.0.2.21"}}
	result, err = r.nodeProvision(context.Background(), request)
	if err != nil || result.Install.Params.PanelIP != "192.0.2.21" {
		t.Fatal("manual correction was not saved", err)
	}
	saved, err = r.loadNodeJob(j.ID)
	if err != nil || saved.RealityKey != j.RealityKey {
		t.Fatal("address confirmation changed saved keys", err)
	}
	saved.Completed[0] = true
	if err := r.saveNodeJob(&saved); err != nil {
		t.Fatal(err)
	}
	if _, err := r.nodeProvision(context.Background(), request); err == nil {
		t.Fatal("changed firewall source after installation started")
	}
}
