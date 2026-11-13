package installer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func certificateMethod(p services.PanelParameters) string {
	if p.Certificate.Method == "" {
		return "acme"
	}
	return p.Certificate.Method
}

func ValidateCertificateParameters(p services.PanelParameters) error {
	if p.Proxy == "caddy" {
		return nil
	}
	c := p.Certificate
	switch certificateMethod(p) {
	case "acme":
		return nil
	case "cloudflare", "gcore", "bunny":
		if strings.TrimSpace(c.Token) == "" || strings.ContainsAny(c.Token, "\r\n\x00") {
			return errors.New("a valid DNS API token is required")
		}
	case "custom":
		if c.CertificateFile == "" || c.KeyFile == "" {
			return errors.New("certificate and private key paths are required")
		}
	default:
		return errors.New("unsupported certificate method")
	}
	return nil
}

func validateCertificateFiles(certFile, keyFile string, domains []string) error {
	for _, path := range []string{certFile, keyFile} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("certificate and key must be regular files")
		}
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err = certificateUsablePair(cert); err != nil {
		return err
	}
	for _, domain := range domains {
		if err = cert.VerifyHostname(domain); err != nil {
			return err
		}
	}
	return nil
}

func (r *Real) checkCertificateParameters(ctx context.Context, p services.PanelParameters) error {
	if err := ValidateCertificateParameters(p); err != nil {
		return err
	}
	if p.Proxy != "nginx" {
		return nil
	}
	c := p.Certificate
	if certificateMethod(p) == "custom" {
		if err := validateCertificateFiles(c.CertificateFile, c.KeyFile, proxyDomains(p)); err != nil {
			return fmt.Errorf("panel certificate: %w", err)
		}
		if p.RemoteSubscription != nil {
			cert, key := remoteCertificateFiles(c)
			if err := validateCertificateFiles(cert, key, []string{p.SubscriptionDomain}); err != nil {
				return fmt.Errorf("subscription certificate: %w", err)
			}
		}
		return nil
	}
	if certificateMethod(p) == "acme" {
		return nil
	}
	domains := proxyDomains(p)
	if p.RemoteSubscription != nil {
		domains = append(domains, p.SubscriptionDomain)
	}
	for _, domain := range domains {
		if err := r.checkDNSZone(ctx, certificateMethod(p), c.Token, domain); err != nil {
			return err
		}
	}
	return nil
}

func remoteCertificateFiles(c services.CertificateParameters) (string, string) {
	cert, key := c.RemoteCertificateFile, c.RemoteKeyFile
	if cert == "" {
		cert = c.CertificateFile
	}
	if key == "" {
		key = c.KeyFile
	}
	return cert, key
}

// Read-only provider calls validate token and zone visibility before any TXT
// changes. DNS write permission is finally verified by successful DNS-01.
func (r *Real) checkDNSZone(ctx context.Context, provider, token, domain string) error {
	endpoint, header, prefix := "", "", ""
	switch provider {
	case "cloudflare":
		endpoint = "https://api.cloudflare.com/client/v4/zones?per_page=50"
		header = "Authorization"
		prefix = "Bearer "
	case "gcore":
		endpoint = "https://api.gcore.com/dns/v2/zones?limit=1000"
		header = "Authorization"
		prefix = "APIKey "
	case "bunny":
		endpoint = "https://api.bunny.net/dnszone?page=1&perPage=1000"
		header = "AccessKey"
	default:
		return errors.New("unsupported DNS provider")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set(header, prefix+token)
	response, err := r.Client.Do(req)
	if err != nil {
		return fmt.Errorf("%s DNS API request failed", provider)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s DNS API rejected credentials or zone access (HTTP %d)", provider, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return errors.New("cannot read DNS API response")
	}
	var doc any
	if json.Unmarshal(data, &doc) != nil {
		return errors.New("invalid DNS API response")
	}
	names := []string{}
	var visit func(any)
	visit = func(v any) {
		switch value := v.(type) {
		case map[string]any:
			for key, item := range value {
				if key == "name" || key == "Name" || key == "Domain" {
					if name, ok := item.(string); ok {
						names = append(names, strings.ToLower(name))
					}
				}
				visit(item)
			}
		case []any:
			for _, item := range value {
				visit(item)
			}
		}
	}
	visit(doc)
	for _, zone := range names {
		if domain == zone || strings.HasSuffix(domain, "."+zone) {
			return nil
		}
	}
	return fmt.Errorf("%s token cannot see the DNS zone for %s; check zone scope and account pagination", provider, domain)
}

func (r *Real) Preflight(ctx context.Context, p services.PanelParameters) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ValidateParameters(p); err != nil {
		return "", err
	}
	if err := r.checkCertificateParameters(ctx, p); err != nil {
		return "", err
	}
	if err := r.preflight(ctx, p); err != nil {
		return "", err
	}
	return "✓ DNS resolved / DNS разрешён\n✓ SSH ports and proxy ports / SSH и порты прокси\n✓ SSL parameters / Параметры SSL\nDNS-01 write permission and public HTTPS are verified during installation.\nПрава записи DNS-01 и публичный HTTPS проверяются при установке.", nil
}
