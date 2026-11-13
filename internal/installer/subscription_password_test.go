package installer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"golang.org/x/crypto/ssh"
)

func TestSubscriptionPasswordPinnedSSH(t *testing.T) {
	for _, scenario := range []string{"success", "bad-password", "changed-key", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			signer, _ := ssh.NewSignerFromKey(key)
			config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
				if meta.User() != "root" || string(password) != "fixture-password" {
					return nil, errors.New("denied")
				}
				return nil, nil
			}}
			config.AddHostKey(signer)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				server, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(reqs)
				for request := range chans {
					channel, requests, err := request.Accept()
					if err != nil {
						return
					}
					defer channel.Close()
					for req := range requests {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						var command struct{ Command string }
						ssh.Unmarshal(req.Payload, &command)
						if command.Command != "bash -s" {
							req.Reply(false, nil)
							return
						}
						req.Reply(true, nil)
						data, _ := io.ReadAll(channel)
						if string(data) != "printf ready" {
							return
						}
						io.WriteString(channel, "ready")
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
				}
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			number, _ := strconv.Atoi(port)
			remote := services.SubscriptionRemote{SSH: services.NodeSSH{Host: host, Port: number, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}, User: "root", Password: "fixture-password"}
			if scenario == "bad-password" {
				remote.Password = "wrong"
			}
			if scenario == "changed-key" {
				_, other, _ := ed25519.GenerateKey(rand.Reader)
				signer, _ := ssh.NewSignerFromKey(other)
				remote.SSH.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			output, err := runSubscriptionPassword(ctx, remote, []byte("printf ready"))
			listener.Close()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("SSH cancellation leaked a connection")
			}
			if scenario == "success" {
				if err != nil || string(output) != "ready" {
					t.Fatal(err, string(output))
				}
			} else if err == nil {
				t.Fatal("SSH failure accepted")
			}
		})
	}
}
