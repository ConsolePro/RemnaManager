package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"golang.org/x/crypto/ssh"
)

// Password authentication runs inside Go: no argv, environment, shell history or
// temporary password file. The accepted host key is mandatory on every session.
func runSubscriptionPassword(ctx context.Context, remote services.SubscriptionRemote, input []byte) ([]byte, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(remote.SSH.HostKey))
	if err != nil {
		return nil, errors.New("confirm the subscription SSH host key first")
	}
	address := net.JoinHostPort(remote.SSH.Host, strconv.Itoa(remote.SSH.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	clientConn, chans, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: remote.User, Auth: []ssh.AuthMethod{ssh.Password(remote.Password)}, HostKeyCallback: ssh.FixedHostKey(key)})
	if err != nil {
		return nil, errors.New("subscription SSH authentication or host-key verification failed")
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConn, chans, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var output subscriptionOutput
	session.Stdin = bytes.NewReader(input)
	session.Stdout = &output
	session.Stderr = &output
	command := "bash -s"
	if remote.User != "root" {
		command = "sudo -n bash -s"
	}
	err = session.Run(command)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		err = fmt.Errorf("subscription SSH operation failed: %w", err)
	}
	return output.Bytes(), err
}

type subscriptionOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *subscriptionOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *subscriptionOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}
