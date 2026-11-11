//go:build linux

package installer

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

type realACMENodeRunner struct{ exec ExecRunner }

func (r realACMENodeRunner) Run(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	if name != "ssh" {
		return nil, fmt.Errorf("real ACME fixture only accepts SSH agent payloads")
	}
	// Only the CA endpoint and its trusted test root differ from production.
	// The downloaded, checksum-verified ACME script and HTTP-01 flow are real.
	input = bytes.ReplaceAll(input, []byte("--server letsencrypt"), []byte("--server https://localhost:14000/dir --ca-bundle /opt/pebble-ca.pem"))
	return r.exec.Run(ctx, input, "bash", "-s")
}

func TestRealACMENodeIssueRenewAndResume(t *testing.T) {
	if os.Getenv("RM_REAL_ACME_INTEGRATION") != "1" {
		t.Skip("opt-in disposable real acme.sh/Pebble/Nginx test")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	t.Setenv("CA_BUNDLE", "/opt/pebble-ca.pem")
	t.Setenv("SSH_CONNECTION", "192.0.2.20 55000 192.0.2.10 2200")
	config := map[string]any{"pebble": map[string]any{"listenAddress": "127.0.0.1:14000", "managementListenAddress": "127.0.0.1:15000", "certificate": "/opt/pebble-cert.pem", "privateKey": "/opt/pebble-key.pem", "httpPort": 80, "tlsPort": 443, "externalAccountBindingRequired": false, "retryAfter": map[string]int{"authz": 1, "order": 1}, "profiles": map[string]any{"default": map[string]any{"validityPeriod": 2592000}}}}
	configPath := filepath.Join(t.TempDir(), "pebble.json")
	data, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "pebble.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server := exec.CommandContext(ctx, "/opt/pebble", "-config", configPath)
	server.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_AUTHZREUSE=0", "PEBBLE_WFE_NONCEREJECT=0")
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	trust, err := os.ReadFile("/opt/pebble-ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	serverCA := x509.NewCertPool()
	if !serverCA.AppendCertsFromPEM(trust) {
		t.Fatal("invalid test CA")
	}
	management := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: serverCA, MinVersion: tls.VersionTLS12}}}
	var root []byte
	for attempt := 0; attempt < 30; attempt++ {
		response, e := management.Get("https://localhost:15000/roots/0")
		if e == nil {
			root, e = io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if e == nil && response.StatusCode == 200 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	certificateCA := x509.NewCertPool()
	if !certificateCA.AppendCertsFromPEM(root) {
		t.Fatal("Pebble certificate issuer not ready")
	}
	r := NewReal()
	r.Root = t.TempDir()
	r.Runner = realACMENodeRunner{}
	j := provisionJob(t)
	j.Version = 2
	j.Params.SSH.Port = 2200
	j.Params.APIPort = 3333
	j.Params.Domain = "cover.fixture.test"
	j.Params.SiteTemplate = "studio"
	j.Email = "operator@example.net"
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	run := func(stage int) {
		t.Helper()
		if err := r.runNodeStage(ctx, &j, stage); err != nil {
			agentLog, _ := os.ReadFile("/var/lib/remnamanager-node/install.log")
			safe := strings.ReplaceAll(string(agentLog), j.Secret, "[REDACTED]")
			if len(safe) > 8000 {
				safe = safe[len(safe)-8000:]
			}
			t.Log(safe)
			t.Fatal("real ACME stage", stage, err)
		}
	}
	run(0)
	j.Completed[1] = true
	run(2)
	run(3)
	canonical, err := os.ReadFile("/opt/acme.sh.fixture")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte("\r\n")) {
		t.Fatal("ACME fixture must contain exact upstream LF bytes, not a Windows checkout")
	}
	badSource := "/opt/acme-crlf.fixture"
	if err := os.WriteFile(badSource, bytes.ReplaceAll(canonical, []byte("\n"), []byte("\r\n")), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RM_ACME_DOWNLOAD_SOURCE", badSource)
	if err := r.runNodeStage(ctx, &j, 4); err == nil || !strings.Contains(services.ErrorText(err, "ru"), "SHA256") {
		t.Fatal("corrupt/check-out-converted script was not rejected with a useful error", err)
	}
	if j.Completed[4] {
		t.Fatal("failed checksum stage marked complete")
	}
	if _, err := os.Stat("/var/lib/remnamanager-node/acme/acme.sh"); !os.IsNotExist(err) {
		t.Fatal("unverified script was installed")
	}
	t.Setenv("RM_ACME_DOWNLOAD_SOURCE", "/opt/acme.sh.fixture")
	run(4)
	certPath := "/var/lib/remnamanager-node/ssl/fullchain.pem"
	first, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialTLSContext: func(c context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(c, network, "127.0.0.1:8443")
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprint(conn, "PROXY TCP4 192.0.2.50 192.0.2.10 55000 443\r\n"); err != nil {
			conn.Close()
			return nil, err
		}
		secure := tls.Client(conn, &tls.Config{RootCAs: certificateCA, ServerName: j.Params.Domain, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if err := secure.HandshakeContext(c); err != nil {
			conn.Close()
			return nil, err
		}
		return secure, nil
	}}}
	checkWebsite := func() {
		t.Helper()
		var response *http.Response
		var err error
		for attempt := 0; attempt < 30; attempt++ {
			response, err = client.Get("https://cover.fixture.test/")
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatal("real issued certificate/TLS failed", err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), "Northline Studio") {
			t.Fatal("website not served with real certificate")
		}
	}
	checkWebsite()
	executor := ExecRunner{}
	if _, err := executor.Run(ctx, nil, "systemctl", "start", "remnamanager-node-acme.service"); err != nil {
		t.Fatal("scheduled real ACME cron check failed", err)
	}
	output, err := executor.Run(ctx, nil, "/bin/sh", "/var/lib/remnamanager-node/acme/acme.sh", "--home", "/var/lib/remnamanager-node/acme", "--config-home", "/var/lib/remnamanager-node/acme", "--server", "https://localhost:14000/dir", "--ca-bundle", "/opt/pebble-ca.pem", "--renew", "--ecc", "--force", "-d", j.Params.Domain)
	if err != nil {
		t.Log(string(output))
		t.Fatal("real renewal failed", err)
	}
	second, err := os.ReadFile(certPath)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("real renewal did not replace certificate", err)
	}
	client.Transport.(*http.Transport).CloseIdleConnections()
	checkWebsite()
	beforeKeys := j.RealityKey + strings.Join(j.ShortIDs, ",") + j.Secret
	if _, err := r.runNodeAgent(ctx, j, 3); err != nil {
		agentLog, _ := os.ReadFile("/var/lib/remnamanager-node/install.log")
		t.Log(string(agentLog))
		t.Fatal("real ACME resume failed", err)
	}
	if beforeKeys != j.RealityKey+strings.Join(j.ShortIDs, ",")+j.Secret {
		t.Fatal("resume changed node credentials")
	}
	t.Log("Real acme.sh: canonical SHA256, account registration, HTTP-01 issuance, certificate install, forced renewal/reload and resume succeeded against Pebble; TLS validated with test roots, without --insecure.")
}
