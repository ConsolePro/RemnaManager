//go:build linux

package installer

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Run only in a disposable SSH test container; no host SSH config is edited.
func TestLivePinnedSSHConnection(t *testing.T) {
	if os.Getenv("RM_SSH_INTEGRATION") != "1" {
		t.Skip("opt-in isolated OpenSSH server")
	}
	root := t.TempDir()
	runner := ExecRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range []string{"host-key", "client-key"} {
		if _, err := runner.Run(ctx, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nPermitRootLogin prohibit-password\nPubkeyAuthentication yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\n", port, filepath.Join(root, "host-key"), filepath.Join(root, "sshd.pid"), filepath.Join(root, "client-key.pub"))
	path := filepath.Join(root, "sshd_config")
	if err = os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-e", "-f", path)
	var logs bytes.Buffer
	server.Stderr = &logs
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	r := NewReal()
	r.Root = filepath.Join(root, "manager")
	r.Runner = runner
	r.state, _ = newState(testParams())
	settings := services.NodeSSH{Host: "ssh.fixture.test", Port: port, IdentityFile: filepath.Join(root, "client-key")}
	var prepared services.NodeResult
	for i := 0; i < 10; i++ {
		prepared, err = r.prepareNodeSSH(ctx, testNodeUUID, settings)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("SSH server was not ready", err)
	}
	result, err := r.trustNodeSSH(ctx, testNodeUUID, settings, prepared.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the production connection arguments, removing only the interactive TTY.
	args := []string{}
	for _, arg := range result.Command[1:] {
		if arg != "-t" {
			args = append(args, arg)
		}
	}
	args = append([]string{"-o", "BatchMode=yes"}, args...)
	args = append(args, "printf ssh-verified")
	data, err := runner.Run(ctx, nil, "ssh", args...)
	if err != nil || !strings.HasSuffix(string(data), "ssh-verified") {
		t.Fatal("pinned authenticated SSH failed", err)
	}
	// Corrupt only this fixture's pin. OpenSSH must refuse the same server.
	known := filepath.Join(r.Root, "ssh", sshIdentity(settings.Host, settings.Port), "known_hosts")
	_, err = runner.Run(ctx, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, "wrong-key"))
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := os.ReadFile(filepath.Join(root, "wrong-key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(wrong))
	if len(fields) < 2 {
		t.Fatal("invalid test key")
	}
	if err = writeFile(known, []byte(sshIdentity(settings.Host, settings.Port)+" "+fields[0]+" "+fields[1]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Run(ctx, nil, "ssh", args...); err == nil {
		t.Fatal("OpenSSH accepted a mismatched pinned key on port " + strconv.Itoa(port))
	}
	// Authenticate once, then prove batch stages reuse the native master session.
	j := provisionJob(t)
	j.Params.SSH = r.state.NodeSSH[testNodeUUID]
	if err = r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	auth, err := r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID, Fingerprint: prepared.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = runner.Run(ctx, nil, auth.Command[0], auth.Command[1:]...); err != nil {
		t.Fatal("native installation authentication failed", err)
	}
	if err = os.Rename(settings.IdentityFile, settings.IdentityFile+".held"); err != nil {
		t.Fatal(err)
	}
	batch, err := r.provisionSSHArgs(j, true)
	if err != nil {
		t.Fatal(err)
	}
	batch = append(batch, "root@"+j.Params.SSH.Host, "printf master-session-verified")
	data, err = runner.Run(ctx, nil, "ssh", batch...)
	if err != nil || !strings.HasSuffix(string(data), "master-session-verified") {
		t.Fatal("batch SSH did not reuse the authenticated connection", err)
	}
}
