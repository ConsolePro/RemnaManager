package installer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"gopkg.in/yaml.v3"
)

func writeWizardCertificate(t *testing.T, directory string, names []string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(20), DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	data, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(directory, "fixture.pem"), filepath.Join(directory, "fixture.key")
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data}), 0600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600)
	return certPath, keyPath
}

func TestWizard36ConfigurationsAndIntegrationPayloads(t *testing.T) {
	count := 0
	for _, separate := range []bool{false, true} {
		for _, proxy := range []string{"caddy", "nginx"} {
			methods := []string{"acme"}
			if proxy == "nginx" {
				methods = []string{"acme", "cloudflare", "gcore", "bunny", "custom"}
			}
			for _, method := range methods {
				for _, protection := range []string{"cookie", "tinyauth", "mfa"} {
					count++
					t.Run(strings.Join([]string{map[bool]string{false: "bundled", true: "separate"}[separate], proxy, method, protection}, "/"), func(t *testing.T) {
						p := testParams()
						p.Proxy = proxy
						p.Protection = protection
						p.Certificate.Method = method
						if protection == "tinyauth" {
							p.AuthDomain = "auth." + p.PanelDomain
						}
						if separate {
							p.RemoteSubscription = &services.SubscriptionRemote{SSH: services.NodeSSH{Host: "192.0.2.40", Port: 2222, HostKey: provisionJob(t).Params.SSH.HostKey}, User: "root", Proxy: proxy}
						}
						if method != "acme" && method != "custom" {
							p.Certificate.Token = "fixture-dns-token"
						}
						p.Certificate.CertificateFile, p.Certificate.KeyFile = writeWizardCertificate(t, t.TempDir(), append(proxyDomains(p), p.SubscriptionDomain))
						if err := ValidateParameters(p); err != nil {
							t.Fatal(err)
						}
						s, err := newState(p)
						if err != nil {
							t.Fatal(err)
						}
						s.LayoutVersion = officialLayout
						s.BackendTag = panelTemplateVersion
						s.SubToken = "fixture-subscription-token"
						compose, err := RenderStack(s)
						if err != nil {
							t.Fatal(err)
						}
						var doc map[string]any
						if yaml.Unmarshal(compose, &doc) != nil {
							t.Fatal("invalid panel YAML")
						}
						rendered := renderOfficialProxy(s)
						if !strings.Contains(rendered, p.PanelDomain) || strings.Contains(rendered, "PANEL_DOMAIN") {
							t.Fatal("domain not substituted")
						}
						if strings.Contains(rendered, "SUBSCRIPTION_PAGE_DOMAIN") {
							t.Fatal("subscription domain not substituted")
						}
						if !separate && !strings.Contains(rendered, p.SubscriptionDomain) {
							t.Fatal("missing public subscription")
						}
						if protection == "cookie" && !strings.Contains(rendered, "403") {
							t.Fatal("missing denial")
						}
						r := NewReal()
						shortRoot, e := os.MkdirTemp("", "rmw-")
						if e != nil {
							t.Fatal(e)
						}
						defer os.RemoveAll(shortRoot)
						r.Root = shortRoot
						r.Stack = filepath.Join(t.TempDir(), "stack")
						r.state = s
						f := &fakeRunner{response: func(call string, input []byte) ([]byte, error) {
							if strings.HasPrefix(call, "bash ") {
								if method == "custom" {
									t.Fatal("custom certificate invoked ACME")
								}
								if !strings.Contains(string(input), "RM_CERT_METHOD='"+method+"'") {
									t.Fatal("certificate method lost")
								}
								if !strings.Contains(call, strings.Join(proxyDomains(p), ",")) {
									t.Fatal("SAN domains lost")
								}
								cert, key := writeWizardCertificate(t, t.TempDir(), proxyDomains(p))
								data, _ := os.ReadFile(cert)
								writeFile(filepath.Join(r.Stack, "nginx", "fullchain.pem"), data, 0600)
								data, _ = os.ReadFile(key)
								writeFile(filepath.Join(r.Stack, "nginx", "privkey.key"), data, 0600)
							}
							return []byte("ready"), nil
						}}
						r.Runner = f
						if proxy == "nginx" {
							if err = r.installPanelCertificates(context.Background()); err != nil {
								t.Fatal(err)
							}
						}
						if separate {
							remote, env, config, err := renderRemoteSubscription(s)
							if err != nil {
								t.Fatal(err)
							}
							if strings.Contains(string(remote), "postgres") || strings.Contains(string(remote), "backend:") {
								t.Fatal("second panel installed")
							}
							if !strings.Contains(string(env), "https://"+p.PanelDomain) || !strings.Contains(config, p.SubscriptionDomain) {
								t.Fatal("remote wiring lost")
							}
							if _, err = r.remoteSubscription(context.Background(), "install", ""); err != nil {
								t.Fatal(err)
							}
							if !strings.HasPrefix(f.calls[len(f.calls)-1], "ssh ") {
								t.Fatal("remote command not sent through SSH")
							}
						}
					})
				}
			}
		}
	}
	if count != 36 {
		t.Fatal(count)
	}
}

func TestWizardCredentialsEncryptedAndRedacted(t *testing.T) {
	p := testParams()
	p.Certificate = services.CertificateParameters{Method: "cloudflare", Token: "dns-secret-fixture"}
	p.RemoteSubscription = &services.SubscriptionRemote{Password: "ssh-secret-fixture", User: "root", Proxy: "nginx"}
	s, _ := newState(p)
	path := statePath(t.TempDir())
	if err := saveState(path, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{p.Certificate.Token, p.RemoteSubscription.Password} {
		if strings.Contains(string(data), secret) {
			t.Fatal("plaintext secret persisted")
		}
	}
	loaded, err := loadState(path)
	if err != nil || !reflect.DeepEqual(s, loaded) {
		t.Fatal("credential roundtrip", err)
	}
	r := NewReal()
	r.state = s
	if text := r.safeOutput([]byte(p.Certificate.Token + " " + p.RemoteSubscription.Password)); strings.Contains(text, "secret-fixture") {
		t.Fatal("credential leaked")
	}
}

func TestOfficialDownloadsFailClosedAndReuseVerifiedCache(t *testing.T) {
	r := NewReal()
	r.Root = t.TempDir()
	r.state, _ = newState(testParams())
	calls := 0
	corrupt := true
	r.Client = &http.Client{Transport: protectionTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		name := filepath.Base(req.URL.Path)
		fixture := "panel.compose.yml"
		if name == ".env.sample" {
			fixture = "panel.env"
		}
		body := templateFile(fixture)
		if corrupt {
			body = "not official"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	if err := r.downloadPanelArtifacts(context.Background()); err == nil {
		t.Fatal("corrupt artifact accepted")
	}
	corrupt = false
	if err := r.downloadPanelArtifacts(context.Background()); err != nil {
		t.Fatal(err)
	}
	previous := calls
	if err := r.downloadPanelArtifacts(context.Background()); err != nil || calls != previous {
		t.Fatal("verified source cache not reused", err)
	}
}

func TestDNSPreflightRejectsBadTokenAndMissingZone(t *testing.T) {
	for _, provider := range []string{"cloudflare", "gcore", "bunny"} {
		for _, scenario := range []string{"unauthorized", "no-zone", "valid"} {
			r := NewReal()
			r.Client = &http.Client{Transport: protectionTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.String(), "secret") {
					t.Fatal("token in URL")
				}
				status := 200
				body := `{"result":[{"name":"example.com"}]}`
				if scenario == "unauthorized" {
					status = 403
				}
				if scenario == "no-zone" {
					body = `{"result":[]}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			err := r.checkDNSZone(context.Background(), provider, "secret", "panel.example.com")
			if (err == nil) != (scenario == "valid") {
				t.Fatal(provider, scenario, err)
			}
		}
	}
}

func TestCertificateFilesRejectMismatchAndWrongNames(t *testing.T) {
	cert, key := writeWizardCertificate(t, t.TempDir(), []string{"*.example.com"})
	for _, names := range [][]string{{"example.com"}, {"deep.sub.example.com"}, {"panel.other.test"}} {
		if err := validateCertificateFiles(cert, key, names); err == nil {
			t.Fatal("wildcard incorrectly covered", names)
		}
	}
	if err := validateCertificateFiles(cert, key, []string{"panel.example.com", "sub.example.com"}); err != nil {
		t.Fatal(err)
	}
	_, wrongKey := writeWizardCertificate(t, t.TempDir(), []string{"panel.example.com"})
	if err := validateCertificateFiles(cert, wrongKey, []string{"panel.example.com"}); err == nil {
		t.Fatal("wrong key accepted")
	}
	if err := validateCertificateFiles("missing", key, []string{"panel.example.com"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
