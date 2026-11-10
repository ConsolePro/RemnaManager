package installer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

const testNodeUUID = "11111111-2222-4333-8444-555555555555"

func nodeTestAPI(t *testing.T, r *Real, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+r.state.ManagerToken || req.Header.Get("X-Forwarded-Proto") != "https" {
			t.Error("missing node API authentication / proxy headers")
		}
		handler(w, req)
	}))
	t.Cleanup(server.Close)
	r.APIBase = server.URL + "/api"
	r.Client = server.Client()
}

func TestNodeListObjectAndArrayEnvelopes(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		r, _ := managementFixture(t)
		nodeTestAPI(t, r, func(w http.ResponseWriter, req *http.Request) {
			list := []services.Node{{UUID: testNodeUUID, Name: "Тестовая нода", Address: "192.0.2.10", Port: 2222}}
			var response any = list
			if wrapped {
				response = map[string]any{"nodes": list}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": response})
		})
		result, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "nodes-list"})
		if err != nil || len(result.Nodes) != 1 || result.Nodes[0].UUID != testNodeUUID {
			t.Fatal(wrapped, err, result)
		}
	}
}

func TestNodeChangesUseUUIDAndDoNotInvertStateTwice(t *testing.T) {
	r, _ := managementFixture(t)
	disabled := false
	posts, patches, restarts := 0, 0, 0
	name := "Тестовая нода"
	nodeTestAPI(t, r, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "GET" && req.URL.Path == "/api/nodes/"+testNodeUUID:
			json.NewEncoder(w).Encode(map[string]any{"response": services.Node{UUID: testNodeUUID, Name: name, Address: "192.0.2.10", Port: 2222, IsConnected: true, IsDisabled: disabled}})
		case req.Method == "PATCH" && req.URL.Path == "/api/nodes":
			var body map[string]string
			json.NewDecoder(req.Body).Decode(&body)
			if body["uuid"] != testNodeUUID {
				t.Fatal("wrong rename target")
			}
			name = body["name"]
			patches++
			io.WriteString(w, `{"response":{}}`)
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/actions/disable"):
			disabled = true
			posts++
			io.WriteString(w, `{"response":{}}`)
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/actions/restart"):
			var body map[string]bool
			json.NewDecoder(req.Body).Decode(&body)
			if !body["forceRestart"] {
				t.Error("missing restart body")
			}
			restarts++
			w.WriteHeader(202)
		default:
			t.Error("unexpected request", req.Method, req.URL.Path)
			w.WriteHeader(404)
		}
	})
	for i := 0; i < 2; i++ {
		if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-toggle", UUID: testNodeUUID, Value: "disable"}); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 1 {
		t.Fatal("repeated request inverted the node state")
	}
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-restart", UUID: testNodeUUID}); err == nil || restarts != 0 {
		t.Fatal("restarted disabled node")
	}
	disabled = false
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-restart", UUID: testNodeUUID}); err != nil || restarts != 1 {
		t.Fatal("empty 202 response rejected", err)
	}
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-rename", UUID: testNodeUUID, Value: "Новое имя"}); err != nil || patches != 1 || name != "Новое имя" {
		t.Fatal(err)
	}
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-rename", UUID: "../../users", Value: "Wrong target"}); err == nil {
		t.Fatal("path injection accepted")
	}
}

func TestNodeSSHRequiresExactCurrentFingerprint(t *testing.T) {
	r, f := managementFixture(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := private.Public().(ed25519.PublicKey)
	wire := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	wire = append(wire, 0, 0, 0, 32)
	wire = append(wire, public...)
	encoded := base64.StdEncoding.EncodeToString(wire)
	sum := sha256.Sum256(wire)
	expected := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	key := "ssh-ed25519 " + encoded
	f.response = func(call string, _ []byte) ([]byte, error) {
		if !strings.HasPrefix(call, "ssh-keyscan ") {
			return nil, errors.New("unexpected command")
		}
		return []byte("# banner\nnode.example.test " + key + "\n"), nil
	}
	settings := services.NodeSSH{Host: "node.example.test", Port: 2200}
	prepared, err := r.prepareNodeSSH(context.Background(), testNodeUUID, settings)
	if err != nil || prepared.Fingerprint != expected || prepared.HostKeyChanged {
		t.Fatal(prepared, err)
	}
	if _, err := r.trustNodeSSH(context.Background(), testNodeUUID, settings, "SHA256:wrong"); err == nil {
		t.Fatal("wrong fingerprint trusted")
	}
	if len(r.state.NodeSSH) != 0 {
		t.Fatal("unconfirmed key persisted")
	}
	trusted, err := r.trustNodeSSH(context.Background(), testNodeUUID, settings, expected)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(trusted.Command, " ")
	for _, required := range []string{"StrictHostKeyChecking=yes", "ForwardAgent=no", "-F /dev/null", "root@node.example.test", "HostKeyAlias="} {
		if !strings.Contains(command, required) {
			t.Fatal(required, command)
		}
	}
	saved, _ := loadState(statePath(r.Root))
	if saved.NodeSSH[testNodeUUID].HostKey != key {
		t.Fatal("pin was not saved")
	}
	known := filepath.Join(r.Root, "ssh", sshIdentity(settings.Host, settings.Port), "known_hosts")
	before, _ := os.ReadFile(known)
	// A server key change after preview must invalidate the earlier confirmation.
	wire[len(wire)-1] ^= 1
	key = "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire)
	prepared, err = r.prepareNodeSSH(context.Background(), testNodeUUID, settings)
	if err != nil || !prepared.HostKeyChanged {
		t.Fatal("changed key was not flagged", err)
	}
	if _, err = r.trustNodeSSH(context.Background(), testNodeUUID, settings, expected); err == nil {
		t.Fatal("stale preview fingerprint was accepted")
	}
	after, _ := os.ReadFile(known)
	if string(before) != string(after) {
		t.Fatal("changed host key replaced the pin without confirmation")
	}
}

func TestNodeInputsRejectInjection(t *testing.T) {
	for _, value := range []string{"ab", "abc\nxyz", strings.Repeat("я", 31), " name "} {
		if ValidateNodeName(value) == nil {
			t.Fatal(value)
		}
	}
	if err := ValidateNodeName("Нода Москва"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"-oProxyCommand=id", "user@node.test", "node.test;id", "https://node.test", "127.0.0.1", "::"} {
		if ValidateNodeSSH(services.NodeSSH{Host: host, Port: 22}) == nil {
			t.Fatal(host)
		}
	}
	for _, key := range []string{"bad", base64.StdEncoding.EncodeToString(make([]byte, 51))} {
		if _, err := hostKeyFingerprint(key); err == nil {
			t.Fatal("invalid host key accepted")
		}
	}
	if fmt.Sprint(sshIdentity("node.test", 22)) == sshIdentity("node.test", 2200) {
		t.Fatal("SSH ports share a trust record")
	}
}
