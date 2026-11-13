package installer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteSubscriptionAPIGate(t *testing.T) {
	root := os.Getenv("RM_PROTECTION_FIXTURE")
	if root == "" || os.Getenv("RM_PROTECTION_REMOTE") != "1" {
		t.Skip("opt-in remote subscription API fixture")
	}
	cert, err := os.ReadFile(filepath.Join(root, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)
	for _, proxy := range []string{"nginx", "caddy"} {
		for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
			t.Run(proxy+"/"+mode, func(t *testing.T) {
				endpoint := os.Getenv("RM_PROTECTION_" + strings.ToUpper(proxy+"_"+mode))
				tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, endpoint)
				}}
				defer tr.CloseIdleConnections()
				client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				for _, probe := range []struct {
					method, path, token string
					allow               bool
				}{{"GET", "/api/system/stats", "fixture-subscription-token", true}, {"GET", "/api/subscriptions/test", "fixture-subscription-token", true}, {"GET", "/api/subscription-page-configs", "fixture-subscription-token", true}, {"GET", "/api/nodes", "fixture-subscription-token", false}, {"GET", "/api/auth/status", "fixture-subscription-token", false}, {"GET", "/api/system/stats", "wrong-token", false}, {"POST", "/api/subscriptions/test", "fixture-subscription-token", false}} {
					req, _ := http.NewRequest(probe.method, "https://panel.example.test"+probe.path, nil)
					req.Header.Set("Authorization", "Bearer "+probe.token)
					resp, e := client.Do(req)
					if e != nil {
						t.Fatal(e)
					}
					resp.Body.Close()
					if probe.allow && resp.StatusCode != 200 {
						t.Fatalf("subscription API %s: %d", probe.path, resp.StatusCode)
					}
					if !probe.allow && resp.StatusCode != 403 && resp.StatusCode != 302 && resp.StatusCode != 401 {
						t.Fatalf("protection bypass %s %s: %d", probe.method, probe.path, resp.StatusCode)
					}
				}
			})
		}
	}
}
