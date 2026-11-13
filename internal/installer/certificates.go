package installer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"os"
	"path/filepath"
	"time"
)

func panelCA(p services.PanelParameters) string {
	if p.CertificateAuthority == "" {
		return "letsencrypt"
	}
	return p.CertificateAuthority
}

func certificateUsable(directory, domain string) error {
	for _, name := range []string{"fullchain.pem", "privkey.pem"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("certificate files must be regular files")
		}
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "fullchain.pem"), filepath.Join(directory, "privkey.pem"))
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return errors.New("certificate is not currently valid")
	}
	return cert.VerifyHostname(domain)
}
