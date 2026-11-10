//go:build linux

package installer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type localNodeAgentRunner struct{ exec ExecRunner }

func (r localNodeAgentRunner) Run(ctx context.Context, in []byte, name string, args ...string) ([]byte, error) {
	if name != "ssh" {
		return nil, errors.New("agent stage fixture only accepts SSH payloads")
	}
	return r.exec.Run(ctx, in, "bash", "-s")
}

func TestNodeAgentStagesInDisposableContainer(t *testing.T) {
	if os.Getenv("RM_AGENT_STAGES_INTEGRATION") != "1" {
		t.Skip("opt-in isolated agent command fixtures")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("agent stage fixture requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Setenv("SSH_CONNECTION", "198.51.100.1 55000 192.0.2.10 2200")
	r := NewReal()
	r.Root = t.TempDir()
	r.Runner = localNodeAgentRunner{}
	j := provisionJob(t)
	j.Params.Domain = "cover.fixture.test"
	j.Params.SSH.Port = 2200
	occupied, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.runNodeStage(ctx, &j, 0); err == nil {
		occupied.Close()
		t.Fatal("preflight accepted an occupied reserved port")
	}
	occupied.Close()
	if _, err = os.Stat("/var/lib/remnamanager-node/preflight-ok"); !os.IsNotExist(err) {
		t.Fatal("failed preflight was marked complete")
	}
	for stage := 0; stage <= 3; stage++ {
		t.Log("remote stage:", stage)
		if err := r.runNodeStage(ctx, &j, stage); err != nil {
			log, _ := os.ReadFile("/var/lib/remnamanager-node/install.log")
			text := strings.ReplaceAll(string(log), j.Secret, "[REDACTED]")
			if len(text) > 6000 {
				text = text[len(text)-6000:]
			}
			t.Log(text)
			t.Fatal(err)
		}
	}
	composeBefore, err := os.ReadFile("/opt/remnanode/compose.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(composeBefore), j.Secret) || !strings.Contains(string(composeBefore), "@sha256:") {
		t.Fatal("pinned image/secret missing from real agent files")
	}
	certificate, err := os.ReadFile("/etc/letsencrypt/live/cover.fixture.test/fullchain.pem")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid fixture CA")
	}
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(c context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, network, "127.0.0.1:8443")
	}}}
	response, err := client.Get("https://cover.fixture.test:8443/")
	if err != nil {
		t.Fatal("real Nginx/selfsteal TLS failed", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "North Studio") {
		t.Fatal("selfsteal website unavailable")
	}
	for i := 4; i <= 6; i++ {
		j.Completed[i] = true
	} // API stages have a separate real-backend test.
	if err = r.runNodeStage(ctx, &j, 7); err != nil {
		t.Fatal("firewall trial failed", err)
	}
	if !j.FirewallPending || time.Until(j.RollbackAt) < 90*time.Second {
		t.Fatal("missing live rollback deadline")
	}
	if err = os.WriteFile("/var/lib/remnamanager-node/firewall-pending", []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.runNodeAgent(ctx, j, 8); err == nil {
		t.Fatal("expired confirmation was accepted")
	}
	exec := ExecRunner{}
	if _, err = exec.Run(ctx, nil, "systemctl", "fixture-expire"); err != nil {
		t.Fatal(err)
	}
	rules, err := exec.Run(ctx, nil, "nft", "list", "table", "inet", "remnamanager_node")
	if err != nil || !strings.Contains(string(rules), "policy accept") {
		t.Fatal("unconfirmed trial did not restore the API-only policy", err)
	}
	if err = r.runNodeStage(ctx, &j, 7); err != nil {
		t.Fatal("trial could not be resumed", err)
	}
	if _, err = r.runNodeAgent(ctx, j, 8); err != nil {
		t.Fatal("valid confirmation failed", err)
	}
	active, err := os.ReadFile("/var/lib/remnamanager-node/firewall-active.nft")
	if err != nil || !strings.Contains(string(active), "policy drop") {
		t.Fatal("confirmed policy was not persisted")
	}
	if _, err = r.runNodeAgent(ctx, j, 0); err != nil {
		t.Fatal("preflight resume failed", err)
	}
	if _, err = r.runNodeAgent(ctx, j, 3); err != nil {
		t.Fatal("remote configuration resume failed", err)
	}
	composeAfter, _ := os.ReadFile("/opt/remnanode/compose.json")
	if string(composeBefore) != string(composeAfter) {
		t.Fatal("resume changed node credentials or image")
	}
	for _, path := range []string{"/var/lib/remnamanager-node/job-id", "/var/lib/remnamanager-node/ready", "/etc/systemd/system/remnamanager-node-firewall.service", "/etc/letsencrypt/renewal-hooks/deploy/remnamanager-node"} {
		if _, err = os.Stat(filepath.Clean(path)); err != nil {
			t.Fatal("missing agent artifact", path, err)
		}
	}
}
