package installer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func ValidateNodeSSH(settings services.NodeSSH) error {
	if settings.Port < 1 || settings.Port > 65535 {
		return errors.New("SSH port must be between 1 and 65535")
	}
	ip := net.ParseIP(settings.Host)
	if ip == nil && !ValidDomain(settings.Host) {
		return errors.New("SSH address must be an IP address or a DNS name without scheme, path or username")
	}
	if ip != nil && (ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLoopback()) {
		return errors.New("use a unicast remote SSH address")
	}
	if settings.IdentityFile != "" {
		if !filepath.IsAbs(settings.IdentityFile) || strings.IndexFunc(settings.IdentityFile, unicode.IsControl) >= 0 {
			return errors.New("SSH identity must be an absolute path without control characters")
		}
	}
	return nil
}

func sshIdentity(host string, port int) string {
	sum := sha256.Sum256([]byte(net.JoinHostPort(host, strconv.Itoa(port))))
	return "remnamanager-" + hex.EncodeToString(sum[:12])
}

func hostKeyFingerprint(encoded string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 51 || string(key[4:15]) != "ssh-ed25519" || key[0] != 0 || key[1] != 0 || key[2] != 0 || key[3] != 11 || key[15] != 0 || key[16] != 0 || key[17] != 0 || key[18] != 32 {
		return "", errors.New("invalid SSH Ed25519 host key")
	}
	sum := sha256.Sum256(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

func (r *Real) scanNodeSSH(ctx context.Context, settings services.NodeSSH) (string, string, error) {
	if err := ValidateNodeSSH(settings); err != nil {
		return "", "", err
	}
	if settings.IdentityFile != "" {
		info, err := os.Stat(settings.IdentityFile)
		if err != nil || !info.Mode().IsRegular() {
			return "", "", errors.New("SSH identity file is not readable or not a regular file")
		}
	}
	data, err := r.Runner.Run(ctx, nil, "ssh-keyscan", "-T", "10", "-p", strconv.Itoa(settings.Port), "-t", "ed25519", settings.Host)
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "ssh-ed25519" || strings.HasPrefix(fields[0], "#") {
			continue
		}
		fingerprint, err := hostKeyFingerprint(fields[2])
		if err != nil {
			return "", "", err
		}
		return "ssh-ed25519 " + fields[2], fingerprint, nil
	}
	return "", "", errors.New("SSH server did not provide an Ed25519 host key")
}

func (r *Real) prepareNodeSSH(ctx context.Context, uuid string, settings services.NodeSSH) (services.NodeResult, error) {
	key, fingerprint, err := r.scanNodeSSH(ctx, settings)
	if err != nil {
		return services.NodeResult{}, err
	}
	old := r.state.NodeSSH[uuid]
	settings.HostKey = ""
	return services.NodeResult{SSH: settings, Fingerprint: fingerprint, HostKeyChanged: old.HostKey != "" && (old.HostKey != key || old.Host != settings.Host || old.Port != settings.Port)}, nil
}

func (r *Real) trustNodeSSH(ctx context.Context, uuid string, settings services.NodeSSH, entered string) (services.NodeResult, error) {
	key, fingerprint, err := r.scanNodeSSH(ctx, settings)
	if err != nil {
		return services.NodeResult{}, err
	}
	if entered != fingerprint {
		return services.NodeResult{}, errors.New("SSH fingerprint does not match the current server key; no trust record was changed")
	}
	settings.HostKey = key
	alias := sshIdentity(settings.Host, settings.Port)
	directory := filepath.Join(r.Root, "ssh", alias)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return services.NodeResult{}, err
	}
	known := filepath.Join(directory, "known_hosts")
	if err = writeFile(known, []byte(alias+" "+key+"\n"), 0600); err != nil {
		return services.NodeResult{}, err
	}
	if r.state.NodeSSH == nil {
		r.state.NodeSSH = map[string]services.NodeSSH{}
	}
	r.state.NodeSSH[uuid] = settings
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return services.NodeResult{}, err
	}
	args := []string{"ssh", "-F", "/dev/null", "-t", "-p", strconv.Itoa(settings.Port), "-o", "UserKnownHostsFile=" + known, "-o", "GlobalKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlias=" + alias, "-o", "CheckHostIP=no", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ProxyCommand=none", "-o", "ProxyJump=none", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	if settings.IdentityFile != "" {
		args = append(args, "-i", settings.IdentityFile, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, "root@"+settings.Host)
	return services.NodeResult{SSH: services.NodeSSH{Host: settings.Host, Port: settings.Port, IdentityFile: settings.IdentityFile}, Fingerprint: fingerprint, Command: args}, nil
}
