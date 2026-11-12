package installer

import (
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveNodeV2APIAndRealityWebsite(t *testing.T) {
	root, base, project := os.Getenv("RM_PANEL_FIXTURE"), os.Getenv("RM_PANEL_API"), os.Getenv("RM_NODE_PROJECT")
	if os.Getenv("RM_NODE_V2_INTEGRATION") != "1" || root == "" || base == "" || !strings.HasPrefix(project, "rmgo-nodes-test-") {
		t.Skip("opt-in disposable real panel/node/website stack")
	}
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Stack = root
	r.APIBase = base
	r.Runner = dockerProjectRunner{project: project, stack: root, real: r}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	defer r.Close()
	var err error
	r.state, err = loadState(statePath(r.Root))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	j := provisionJob(t)
	j.Version = 2
	j.ID = strings.Repeat("c", 32)
	j.Params.Name = "Go_V2_Node"
	j.Params.ProfileName = "Go_V2_Profile"
	j.Params.CountryCode = "DE"
	j.Params.APIPort = 3333
	j.Params.SiteTemplate = "atlas"
	j.Params.Address = "node.fixture.test"
	j.Params.Domain = "cover.fixture.test"
	if existing, e := r.loadNodeJob(j.ID); e == nil {
		j = existing
	} else if !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	// The real API must accept the profile first and node registration before
	// the node process exists, as the new wizard requires.
	if err := r.ensureNodeProfile(ctx, &j); err != nil {
		t.Fatal("real v2 profile rejected", err)
	}
	if err := r.ensureNodeRegistration(ctx, &j); err != nil {
		t.Fatal("real pre-deployment node registration rejected", err)
	}
	keys, err := r.nodeAPI(ctx, "GET", "/keygen", nil)
	if err != nil {
		t.Fatal(err)
	}
	var key struct {
		Secret string `json:"secretKey"`
	}
	if json.Unmarshal(keys, &key) != nil || key.Secret == "" {
		t.Fatal("SECRET_KEY missing")
	}
	j.Secret = key.Secret
	path := filepath.Join(root, "compose.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["services"].(map[string]any)["remnanode"].(map[string]any)["environment"] = map[string]string{"NODE_PORT": "3333", "SECRET_KEY": j.Secret}
	data, _ = json.MarshalIndent(doc, "", "  ")
	if err := writeFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.compose(ctx, "--profile", "node-test", "up", "-d", "remnanode", "node-site"); err != nil {
		t.Fatal(err)
	}
	if err := r.waitInstalledNode(ctx, j); err != nil {
		t.Fatal("v2 node did not connect", err)
	}
	if err := r.ensureNodeHost(ctx, &j); err != nil {
		t.Fatal("Firefox host/squad rejected", err)
	}
	if err := r.ensureNodeObjects(ctx, &j); err != nil {
		t.Fatal("v2 recovery changed objects", err)
	}
	raw, err := r.nodeAPI(ctx, "GET", "/nodes/"+j.NodeUUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		Port    int    `json:"port"`
		Country string `json:"countryCode"`
	}
	if json.Unmarshal(raw, &node) != nil || node.Port != 3333 || node.Country != "DE" {
		t.Fatal("real node country/port lost")
	}
	hosts, err := r.nodeObjects(ctx, "/hosts", "hosts")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, host := range hosts {
		if rawString(host["uuid"]) == j.HostUUID {
			found = rawString(host["fingerprint"]) == "firefox"
		}
	}
	if !found {
		t.Fatal("real Firefox host missing")
	}
	manualSquadID := createManualTestSquad(t, r, j)
	userData, err := r.nodeAPI(ctx, "POST", "/users", map[string]any{"username": "rmv2_" + time.Now().Format("150405"), "expireAt": "2099-12-31T00:00:00.000Z", "trafficLimitBytes": 0, "activeInternalSquads": []string{manualSquadID}})
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
	subscription, err := r.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	links, err := io.ReadAll(io.LimitReader(subscription.Body, 1<<20))
	subscription.Body.Close()
	if err != nil || subscription.StatusCode != 200 {
		t.Fatal("subscription unavailable", err)
	}
	if !strings.Contains(string(links), "vless://") {
		links, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(links)))
		if err != nil {
			t.Fatal(err)
		}
	}
	privateBytes, _ := base64.RawURLEncoding.DecodeString(j.RealityKey)
	private, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())
	validLink := false
	for _, line := range strings.Split(string(links), "\n") {
		parsed, err := url.Parse(strings.TrimSpace(line))
		if err != nil || parsed.Scheme != "vless" {
			continue
		}
		query := parsed.Query()
		shortOK := false
		for _, id := range j.ShortIDs {
			shortOK = shortOK || id == query.Get("sid")
		}
		validLink = validLink || (query.Get("fp") == "firefox" && query.Get("pbk") == public && query.Get("sni") == j.Params.Domain && query.Get("flow") == "xtls-rprx-vision" && shortOK)
	}
	if !validLink {
		t.Fatal("real subscription lost Firefox, Reality key, short IDs or Vision")
	}
	cert, err := os.ReadFile(filepath.Join(root, "certs", "panel.test", "fullchain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("bad fixture CA")
	}
	port, err := r.compose(ctx, "--profile", "node-test", "port", "remnanode", "443")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := strings.TrimSpace(string(port))
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: j.Params.Domain, MinVersion: tls.VersionTLS12}, DialContext: func(c context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, network, endpoint)
	}}}
	var response *http.Response
	for attempt := 0; attempt < 30; attempt++ {
		response, err = client.Get("https://cover.fixture.test/")
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatal("ordinary HTTPS did not pass through real Reality to Nginx", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Field Atlas") {
		t.Fatal("real selfsteal website did not load", err, response.StatusCode)
	}
	for _, asset := range []string{"about.html", "style.css", "favicon.svg"} {
		response, err := client.Get("https://cover.fixture.test/" + asset)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("missing real website asset", asset, response.StatusCode)
		}
	}
	t.Log("Real panel accepted v2 profile before node creation, 3–6 short IDs, custom API port, DE country and Firefox host; ordinary HTTPS reached the prepared website through Reality.")
}
