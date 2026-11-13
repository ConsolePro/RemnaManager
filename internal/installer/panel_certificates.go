package installer

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func certificateScript(p services.PanelParameters, script []byte) []byte {
	prefix := "set +x\nexport RM_CERT_METHOD=" + shellQuote(certificateMethod(p)) + "\n"
	if certificateMethod(p) != "acme" && p.Proxy == "nginx" {
		prefix += "export RM_DNS_TOKEN=$(printf '%s' " + shellQuote(base64.StdEncoding.EncodeToString([]byte(p.Certificate.Token))) + " | base64 -d)\n"
	}
	return append([]byte(prefix), script...)
}

func (r *Real) installPanelCertificates(ctx context.Context) error {
	p := r.state.Params
	if certificateMethod(p) == "custom" {
		if err := r.disablePanelACME(ctx, p.PanelDomain); err != nil {
			return err
		}
		if err := validateCertificateFiles(p.Certificate.CertificateFile, p.Certificate.KeyFile, proxyDomains(p)); err != nil {
			return err
		}
		for target, source := range map[string]string{"fullchain.pem": p.Certificate.CertificateFile, "privkey.key": p.Certificate.KeyFile} {
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			if err = writeBoundConfig(filepath.Join(r.Stack, "nginx", target), data); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := r.Runner.Run(ctx, certificateScript(p, panelACMEScript), "bash", "-s", "--", r.Root, r.Stack, p.Email, panelCA(p), strings.Join(proxyDomains(p), ","))
	if err != nil {
		return fmt.Errorf("certificate issuance failed: %w", err)
	}
	return commonCertificateUsable(r.Stack, proxyDomains(p))
}

// Imported certificates must not be overwritten by a previous managed renewal.
func (r *Real) disablePanelACME(ctx context.Context, primary string) error {
	home := filepath.Join(r.Root, "acme")
	if _, err := os.Stat(filepath.Join(home, primary+"_ecc", primary+".conf")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	_, err := r.Runner.Run(ctx, nil, filepath.Join(home, "acme.sh"), "--remove", "--home", home, "--ecc", "-d", primary)
	return err
}
