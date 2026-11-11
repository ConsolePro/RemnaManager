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
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Changes the root password only in the explicitly opted-in disposable test
// container. Never enable this fixture on a real SSH host.
func TestLiveRootPasswordToManagedKey(t *testing.T) {
	if os.Getenv("RM_SSH_PASSWORD_INTEGRATION") != "1" {
		t.Skip("opt-in disposable root-password SSH fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := NewReal()
	r.Root = filepath.Join(t.TempDir(), "manager")
	runner := ExecRunner{}
	r.Runner = runner
	defer r.Close()
	password, err := secret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, []byte("root:"+password+"\n"), "chpasswd"); err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	hostKey := filepath.Join(fixture, "host-key")
	if _, err := runner.Run(ctx, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile .ssh/authorized_keys\nPermitRootLogin yes\nPasswordAuthentication yes\nPubkeyAuthentication yes\nKbdInteractiveAuthentication no\nUsePAM no\n", port, hostKey, filepath.Join(fixture, "sshd.pid"))
	configPath := filepath.Join(fixture, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-e", "-f", configPath)
	server.WaitDelay = time.Second
	var logs bytes.Buffer
	server.Stderr = &logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { r.Close(); server.Process.Kill(); server.Wait() }()
	j := provisionJob(t)
	j.Version = 2
	j.DetailsPending = true
	j.Params.Name = "New_node"
	j.Params.Domain = "pending.invalid"
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	j.Params.SSH = services.NodeSSH{Host: "ssh.fixture.test", Port: port}
	var fingerprint string
	for attempt := 0; attempt < 20; attempt++ {
		j.Params.SSH.HostKey, fingerprint, err = r.scanNodeSSH(ctx, j.Params.SSH)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("SSH fixture not ready", err)
	}
	if err := r.saveNodeJob(&j); err != nil {
		t.Fatal(err)
	}
	auth, err := r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID, Fingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	askpass := filepath.Join(fixture, "askpass")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\nprintf '%s\\n' \"$RM_TEST_SSH_PASSWORD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	login := func(value string) error {
		cmd := exec.CommandContext(ctx, auth.Command[0], auth.Command[1:]...)
		cmd.Env = append(os.Environ(), "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=:0", "RM_TEST_SSH_PASSWORD="+value)
		return cmd.Run()
	}
	if err := login("wrong-fixture-password"); err == nil {
		t.Fatal("invalid root password accepted")
	}
	if err := login(password); err != nil {
		t.Fatal("root password authentication failed", err)
	}
	// Preserve an unrelated key entry while adding the manager key.
	if err := os.MkdirAll("/root/.ssh", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/root/.ssh/authorized_keys", []byte("# existing administrator keys\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-key", JobID: j.ID})
	if err != nil {
		t.Fatal("key bootstrap failed", err)
	}
	if result.Install == nil || result.Install.Params.SSH.IdentityFile == "" {
		t.Fatal("managed identity was not saved")
	}
	j, err = r.loadNodeJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(j.Params.SSH.IdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	authorized, _ := os.ReadFile("/root/.ssh/authorized_keys")
	if !strings.Contains(string(authorized), "# existing administrator keys") || strings.Count(string(authorized), "remnamanager-node-"+j.ID) != 1 {
		t.Fatal("authorized_keys overwritten or duplicated")
	}
	if _, err := runner.Run(ctx, nil, "ssh", "-F", "/dev/null", "-S", r.nodeControl(j.ID), "-O", "exit", "root@"+j.Params.SSH.Host); err != nil {
		t.Fatal(err)
	}
	args, err := r.provisionSSHArgs(j, false)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "-T", "-o", "BatchMode=yes", "-o", "ControlPath=none", "-o", "IdentityAgent=none", "-o", "PreferredAuthentications=publickey", "root@"+j.Params.SSH.Host, "printf managed-key-verified")
	output, err := runner.Run(ctx, nil, "ssh", args...)
	if err != nil || string(output) != "managed-key-verified" {
		t.Fatal("key-only login without password session failed", err)
	}
	// Force the bootstrap path once more with a live key-authenticated master;
	// the same key must be reused and the authorized line must not be duplicated.
	auth, err = r.nodeProvision(ctx, services.NodeRequest{Action: "node-install-auth", JobID: j.ID, Fingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, nil, auth.Command[0], auth.Command[1:]...); err != nil {
		t.Fatal(err)
	}
	j.Params.SSH.IdentityFile = ""
	if err := r.installNodeManagedKey(ctx, &j); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(j.Params.SSH.IdentityFile)
	authorized, _ = os.ReadFile("/root/.ssh/authorized_keys")
	if !bytes.Equal(before, after) || strings.Count(string(authorized), "remnamanager-node-"+j.ID) != 1 {
		t.Fatal("retry changed or duplicated key")
	}
	journal, _ := os.ReadFile(filepath.Join(r.Root, "node-jobs", j.ID+".json"))
	if strings.Contains(string(journal), password) {
		t.Fatal("root password was persisted")
	}
}
