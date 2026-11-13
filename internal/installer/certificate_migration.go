package installer

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (r *Real) importCaddyCertificate(domain string) error {
	root := filepath.Join(r.Stack, "caddy", "data", "caddy", "certificates")
	if r.state.ProxyRuntime != "docker" {
		root = r.systemPath("/var/lib/caddy/.local/share/caddy/certificates")
	}
	if err := noSymlinkParents(root); err != nil {
		return err
	}
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || entry.Name() != domain+".crt" {
			return nil
		}
		cert, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key, err := os.ReadFile(strings.TrimSuffix(path, ".crt") + ".key")
		if err != nil {
			return err
		}
		target := filepath.Join(r.Stack, "nginx", "certs", domain)
		if err = writeFile(filepath.Join(target, "fullchain.pem"), cert, 0600); err != nil {
			return err
		}
		if err = writeFile(filepath.Join(target, "privkey.pem"), key, 0600); err != nil {
			return err
		}
		if err = certificateUsable(target, domain); err != nil {
			return err
		}
		found = true
		return fs.SkipAll
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no reusable Caddy PEM certificate found")
	}
	return nil
}

// Imported certificates remain in use. A daily timer only enrolls them in
// acme.sh when renewal is due, avoiding an unnecessary issue at migration time.
func (r *Real) scheduleImportedCertificates(ctx context.Context) error {
	script := filepath.Join(r.Root, "renew-imported-certificates.sh")
	content := "#!/usr/bin/env bash\nset -Eeuo pipefail\n"
	for _, domain := range proxyDomains(r.state.Params) {
		if certificateUsable(filepath.Join(r.Stack, "nginx", "certs", domain), domain) != nil {
			continue
		}
		content += "if ! openssl x509 -in " + shellQuote(filepath.Join(r.Stack, "nginx", "certs", domain, "fullchain.pem")) + " -noout -checkend 2592000 >/dev/null 2>&1; then\n"
		content += " bash " + shellQuote(filepath.Join(r.Root, "panel-acme.sh")) + " " + shellQuote(r.Root) + " " + shellQuote(r.Stack) + " " + shellQuote(r.state.Params.Email) + " " + shellQuote(panelCA(r.state.Params)) + " " + shellQuote(domain) + "\nfi\n"
	}
	if err := writeFile(filepath.Join(r.Root, "panel-acme.sh"), r.acmeScript(), 0700); err != nil {
		return err
	}
	if err := writeFile(script, []byte(content), 0700); err != nil {
		return err
	}
	// Paths are manager-owned; systemd quoting differs from shell quoting.
	unit := "[Unit]\nDescription=Renew imported RemnaManager TLS certificates\nAfter=docker.service network-online.target\n[Service]\nType=oneshot\nExecStart=/bin/bash \"" + strings.ReplaceAll(script, "\"", "\\\"") + "\"\n"
	if err := writeFile(r.systemPath("/etc/systemd/system/remnamanager-certificates.service"), []byte(unit), 0644); err != nil {
		return err
	}
	timer := "[Unit]\nDescription=Check imported TLS expiry daily\n[Timer]\nOnCalendar=daily\nRandomizedDelaySec=3600\nPersistent=true\n[Install]\nWantedBy=timers.target\n"
	if err := writeFile(r.systemPath("/etc/systemd/system/remnamanager-certificates.timer"), []byte(timer), 0644); err != nil {
		return err
	}
	if _, err := r.Runner.Run(ctx, nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := r.Runner.Run(ctx, nil, "systemctl", "enable", "--now", "remnamanager-certificates.timer")
	return err
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
