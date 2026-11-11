//go:build linux

package installer

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNodeV2AgentWebsiteRenewalAndCustomPort(t *testing.T) {
	if os.Getenv("RM_AGENT_V2_INTEGRATION") != "1" {
		t.Skip("opt-in disposable Nginx/nftables fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Setenv("SSH_CONNECTION", "192.0.2.20 55000 192.0.2.10 2200")
	r := NewReal()
	r.Root = t.TempDir()
	r.Runner = localNodeAgentRunner{}
	j := provisionJob(t)
	j.Version = 2
	j.Params.SSH.Port = 2200
	j.Params.APIPort = 3333
	j.Params.Domain = "cover.fixture.test"
	j.Params.SiteTemplate = "atlas"
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	run := func(stage int) {
		t.Helper()
		if _, err := r.runNodeAgent(ctx, j, stage); err != nil {
			log, _ := os.ReadFile("/var/lib/remnamanager-node/install.log")
			safe := strings.ReplaceAll(string(log), j.Secret, "[REDACTED]")
			if len(safe) > 5000 {
				safe = safe[len(safe)-5000:]
			}
			t.Log(safe)
			t.Fatal("remote stage", stage, err)
		}
	}
	// Exercise the actual Go dispatcher, including logical stage 3 -> agent 2.
	// Profile/node API creation has its own real-backend integration test.
	if err := r.runNodeStage(ctx, &j, 0); err != nil {
		t.Fatal(err)
	}
	j.Completed[1] = true
	for _, stage := range []int{2, 3} {
		if err := r.runNodeStage(ctx, &j, stage); err != nil {
			t.Fatal("Go stage dispatcher", stage, err)
		}
	}
	if _, err := os.Stat("/var/lib/remnamanager-node/ssl/fullchain.pem"); !os.IsNotExist(err) {
		t.Fatal("certificate deployment ran during image pinning")
	}
	if j.Image == "" {
		t.Fatal("image was not pinned before deployment")
	}
	if err := r.runNodeStage(ctx, &j, 4); err != nil {
		log, _ := os.ReadFile("/var/lib/remnamanager-node/install.log")
		t.Log(strings.ReplaceAll(string(log), j.Secret, "[REDACTED]"))
		t.Fatal("deployment stage", err)
	}
	certPath := "/var/lib/remnamanager-node/ssl/fullchain.pem"
	certificate, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid fixture certificate")
	}
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:8443")
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprint(conn, "PROXY TCP4 192.0.2.50 192.0.2.10 54321 443\r\n"); err != nil {
			conn.Close()
			return nil, err
		}
		secure := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: j.Params.Domain, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if err := secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return secure, nil
	}}}
	for _, path := range []string{"/", "/about.html", "/notes.html", "/style.css", "/favicon.svg"} {
		response, err := client.Get("https://cover.fixture.test" + path)
		if err != nil {
			t.Fatal("real selfsteal TLS/PROXY failed", err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || len(body) == 0 {
			t.Fatal("missing website asset", path, response.StatusCode)
		}
	}
	httpClient := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1/", nil)
	request.Host = j.Params.Domain
	var response *http.Response
	for attempt := 0; attempt < 30; attempt++ {
		response, err = httpClient.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 301 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 301 || response.Header.Get("Location") != "https://cover.fixture.test/" {
		t.Fatal("port 80 redirect unavailable", response.StatusCode)
	}
	executor := ExecRunner{}
	if _, err := executor.Run(ctx, nil, "systemctl", "start", "remnamanager-node-acme.service"); err != nil {
		t.Fatal("renewal failed", err)
	}
	renewed, err := os.ReadFile(certPath)
	if err != nil || bytes.Equal(certificate, renewed) {
		t.Fatal("certificate was not replaced", err)
	}
	if timer, err := os.ReadFile("/etc/systemd/system/remnamanager-node-acme.timer"); err != nil || !strings.Contains(string(timer), "OnCalendar=daily") {
		t.Fatal("renewal task missing", err)
	}
	run(7)
	rules, err := executor.Run(ctx, nil, "nft", "list", "table", "inet", "remnamanager_node")
	if err != nil || !strings.Contains(string(rules), "3333") || strings.Contains(string(rules), "2222") {
		t.Fatal("custom API firewall port lost", err, string(rules))
	}
	run(8)
	// Reapplying the same installation must keep the site, credentials and port.
	run(3)
	compose, _ := os.ReadFile("/opt/remnanode/compose.json")
	if !strings.Contains(string(compose), `"3333"`) || !strings.Contains(string(compose), j.Secret) {
		t.Fatal("node port or key changed")
	}
}
