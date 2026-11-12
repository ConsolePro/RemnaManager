package installer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"golang.org/x/crypto/ssh"
)

func (r *Real) checkFreshNodeSSH(ctx context.Context, j NodeJob) error {
	args, err := r.provisionSSHArgs(j, false)
	if err != nil {
		return err
	}
	args = append(args, "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "IdentityAgent=none", "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "root@"+j.Params.SSH.Host, "true")
	_, err = r.Runner.Run(ctx, nil, "ssh", args...)
	return err
}

func (r *Real) resetNodeManagedKey(j *NodeJob) error {
	managed := filepath.Join(r.Root, "ssh", "keys", "node-"+j.ID)
	if j.Params.SSH.IdentityFile == managed {
		if err := noSymlinkParents(managed); err != nil {
			return err
		}
		if err := os.Rename(managed, managed+".previous-"+fmt.Sprint(time.Now().UnixNano())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	j.Params.SSH.IdentityFile = ""
	return r.saveNodeJob(j)
}

func (r *Real) nodeManagedKey(j NodeJob) (string, string, error) {
	if _, err := r.jobPath(j.ID); err != nil {
		return "", "", err
	}
	directory := filepath.Join(r.Root, "ssh", "keys")
	path := filepath.Join(directory, "node-"+j.ID)
	if err := noSymlinkParents(path); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", "", err
		}
		block, err := ssh.MarshalPrivateKey(private, "remnamanager-node-"+j.ID)
		if err != nil {
			return "", "", err
		}
		data = pem.EncodeToMemory(block)
		if err := writeFile(path, data, 0600); err != nil {
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", "", services.LocalizedError("Файл SSH-ключа менеджера должен быть обычным файлом с правами 0600.", "Managed SSH key must be a regular file with private permissions (0600).")
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return "", "", services.LocalizedError("Сохранённый SSH-ключ повреждён. Восстановите его из копии; ключ автоматически не заменяется.", "Saved SSH key is invalid. Restore it from backup; it will not be regenerated.")
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return "", "", fmt.Errorf("managed SSH key must be Ed25519")
	}
	public := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " remnamanager-node-" + j.ID
	return path, public, nil
}

// The public key is generated locally and contains only the Ed25519 wire key
// and a validated hex job ID. Existing authorized_keys entries are preserved.
func installNodePublicKeyScript(public string) []byte {
	return []byte(fmt.Sprintf(`set -eu
umask 077
[ "$(id -u)" -eq 0 ]
node_root_home=$(getent passwd root | cut -d: -f6)
[ -n "$node_root_home" ] && [ -d "$node_root_home" ]
[ ! -L "$node_root_home/.ssh" ]
mkdir -p "$node_root_home/.ssh"
chmod 700 "$node_root_home/.ssh"
node_authorized_keys="$node_root_home/.ssh/authorized_keys"
[ ! -L "$node_authorized_keys" ]
if [ -e "$node_authorized_keys" ]; then
    [ -f "$node_authorized_keys" ]
    [ "$(stat -c %%h "$node_authorized_keys")" -eq 1 ]
fi
: >> "$node_authorized_keys"
chmod 600 "$node_authorized_keys"
chown 0:0 "$node_root_home/.ssh" "$node_authorized_keys"
if ! grep -Fqx -- '%s' "$node_authorized_keys"; then
    printf '\n%%s\n' '%s' >> "$node_authorized_keys"
fi
`, public, public))
}

func (r *Real) installNodeManagedKey(ctx context.Context, j *NodeJob) error {
	if j.Params.SSH.IdentityFile != "" {
		return nil
	}
	path, public, err := r.nodeManagedKey(*j)
	if err != nil {
		return err
	}
	if _, err := r.Runner.Run(ctx, nil, "ssh", "-F", "/dev/null", "-S", r.nodeControl(j.ID), "-O", "check", "root@"+j.Params.SSH.Host); err != nil {
		return services.LocalizedError("Сеанс SSH закрыт. Повторите вход root по паролю, чтобы установить ключ менеджера.", "The SSH session has closed. Authenticate as root with the password again to install the managed key.")
	}
	args, err := r.provisionSSHArgs(*j, true)
	if err != nil {
		return err
	}
	// Require the password-authenticated master. Never start an implicit fresh
	// connection using agent/default keys for this bootstrap operation.
	args = append(args, "-o", "PreferredAuthentications=publickey", "-o", "PubkeyAuthentication=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "root@"+j.Params.SSH.Host, "sh -s")
	if _, err := r.Runner.Run(ctx, installNodePublicKeyScript(public), "ssh", args...); err != nil {
		return services.LocalizedError("Не удалось установить SSH-ключ на ноду через сеанс root. Повторите вход по паролю; существующие ключи не удалялись.", "Could not install the SSH key through the root session. Retry password authentication; existing authorized keys were not removed.")
	}
	verified := *j
	verified.Params.SSH.IdentityFile = path
	args, err = r.provisionSSHArgs(verified, false)
	if err != nil {
		return err
	}
	args = append(args, "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "IdentityAgent=none", "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "root@"+j.Params.SSH.Host, "true")
	if _, err := r.Runner.Run(ctx, nil, "ssh", args...); err != nil {
		return services.LocalizedError("Ключ добавлен, но новый вход по нему не прошёл проверку. Проверьте, разрешён ли вход root по публичному ключу. Повтор установки использует тот же ключ.", "The key was added but a fresh key-only login failed. Check root public-key authentication. Retrying uses the same key.")
	}
	j.Params.SSH.IdentityFile = path
	if err := r.saveNodeJob(j); err != nil {
		return err
	}
	if j.NodeUUID != "" {
		if r.state.NodeSSH == nil {
			r.state.NodeSSH = map[string]services.NodeSSH{}
		}
		r.state.NodeSSH[j.NodeUUID] = j.Params.SSH
		return saveState(statePath(r.Root), r.state)
	}
	return nil
}
