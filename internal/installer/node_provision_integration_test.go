package installer

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveNodeProvisioningAPI(t *testing.T) {
	root, base, project := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_PANEL_API"), os.Getenv("RM_NODE_PROJECT")
	if root == "" || base == "" || !strings.HasPrefix(project, "rmgo-nodes-test-") {
		t.Skip("opt-in disposable node provisioning API")
	}
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.APIBase = base
	r.Runner = dockerProjectRunner{project: project, stack: root, real: r}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	port, err := r.Runner.Run(ctx, nil, "docker", r.composeArgs("port", "remnawave", "3000")...)
	if err != nil || base != "http://"+strings.TrimSpace(string(port))+"/api" {
		t.Fatal("API is not the disposable test project's endpoint")
	}
	r.state, err = loadState(statePath(r.Root))
	if err != nil {
		t.Fatal(err)
	}
	r.lock, err = os.CreateTemp(r.Root, "provision-test-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	j := provisionJob(t)
	j.ID = strings.Repeat("b", 32)
	j.Params.Name = "Go_Provisioned_Node"
	if existing, e := r.loadNodeJob(j.ID); e == nil {
		j = existing
	} else if !os.IsNotExist(e) {
		t.Fatal("existing fixture journal is corrupt", e)
	}
	keys, err := r.nodeAPI(ctx, "GET", "/keygen", nil)
	if err != nil {
		t.Fatal(err)
	}
	var key struct {
		Secret string `json:"secretKey"`
	}
	if json.Unmarshal(keys, &key) != nil || key.Secret == "" {
		t.Fatal("node secret missing")
	}
	j.Secret = key.Secret
	path := filepath.Join(root, "compose.json")
	data, _ := os.ReadFile(path)
	var doc map[string]any
	json.Unmarshal(data, &doc)
	doc["services"].(map[string]any)["remnanode"].(map[string]any)["environment"] = map[string]string{"NODE_PORT": "2222", "SECRET_KEY": j.Secret}
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
	j.Params.Address = strings.TrimSpace(string(address))
	digest, err := r.Runner.Run(ctx, nil, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", "remnawave/node:latest")
	if err != nil {
		t.Fatal(err)
	}
	j.Image = strings.TrimPrefix(strings.TrimSpace(string(digest)), "docker.io/")
	for i := 0; i < 4; i++ {
		j.Completed[i] = true
	}
	if err = r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	if err = r.runNodeStage(ctx, &j, 4); err != nil {
		t.Fatal("Reality profile stage:", err)
	}
	if err = r.runNodeStage(ctx, &j, 5); err != nil {
		t.Fatal("node/host/squad stage:", err)
	}
	if err = r.runNodeStage(ctx, &j, 6); err != nil {
		t.Fatal("real node connection:", err)
	}
	original := []string{j.ProfileUUID, j.InboundUUID, j.NodeUUID, j.HostUUID, j.SquadUUID, j.RealityKey, j.Secret}
	paths := []string{"/config-profiles", "/nodes", "/hosts", "/internal-squads"}
	fields := []string{"configProfiles", "nodes", "hosts", "internalSquads"}
	counts := make([]int, len(paths))
	for i, path := range paths {
		objects, e := r.nodeObjects(ctx, path, fields[i])
		if e != nil {
			t.Fatal(e)
		}
		counts[i] = len(objects)
	}
	// Simulate loss of all API acknowledgements while retaining the original keys.
	j.ProfileUUID = ""
	j.InboundUUID = ""
	j.NodeUUID = ""
	j.HostUUID = ""
	j.SquadUUID = ""
	if err = r.ensureNodeProfile(ctx, &j); err != nil {
		t.Fatal("profile reconciliation:", err)
	}
	if err = r.ensureNodeObjects(ctx, &j); err != nil {
		t.Fatal("object reconciliation:", err)
	}
	for i, value := range []string{j.ProfileUUID, j.InboundUUID, j.NodeUUID, j.HostUUID, j.SquadUUID, j.RealityKey, j.Secret} {
		if value != original[i] {
			t.Fatal("resume changed an API object or key")
		}
	}
	for i, path := range paths {
		objects, e := r.nodeObjects(ctx, path, fields[i])
		if e != nil || len(objects) != counts[i] {
			t.Fatal("resume duplicated an API resource", path, e)
		}
	}
	manualSquadID := createManualTestSquad(t, r, j)
	userData, err := r.nodeAPI(ctx, "POST", "/users", map[string]any{"username": "rm_provision_" + time.Now().Format("150405"), "expireAt": "2099-12-31T00:00:00.000Z", "trafficLimitBytes": 0, "activeInternalSquads": []string{manualSquadID}})
	if err != nil {
		t.Fatal(err)
	}
	var user struct {
		Short string `json:"shortUuid"`
	}
	if json.Unmarshal(userData, &user) != nil || user.Short == "" {
		t.Fatal("test user missing")
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", r.APIBase+"/sub/"+user.Short, nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	request.Header.Set("User-Agent", "curl/8.0")
	request.Host = r.state.Params.PanelDomain
	response, err := r.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("generated subscription is unavailable")
	}
	links := string(body)
	if !strings.Contains(links, "vless://") {
		decoded, e := base64.StdEncoding.DecodeString(strings.TrimSpace(links))
		if e != nil {
			t.Fatal("subscription is not valid base64")
		}
		links = string(decoded)
	}
	privateBytes, _ := base64.RawURLEncoding.DecodeString(j.RealityKey)
	private, _ := ecdh.X25519().NewPrivateKey(privateBytes)
	public := base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())
	valid := false
	for _, line := range strings.Split(links, "\n") {
		u, e := url.Parse(strings.TrimSpace(line))
		if e == nil && u.Scheme == "vless" && u.Query().Get("security") == "reality" && u.Query().Get("pbk") == public && u.Query().Get("sni") == j.Params.Domain && u.Query().Get("flow") == "xtls-rprx-vision" {
			valid = true
		}
	}
	if !valid {
		t.Fatal("generated subscription does not match the saved Reality key/domain")
	}
}
