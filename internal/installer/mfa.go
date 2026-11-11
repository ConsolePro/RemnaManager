package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Official Remnawave MFA portal image, pinned to the tested amd64 manifest.
// The upstream image has no arm64 build; use the same pinned sources there.
const mfaImage = "remnawave/caddy-with-auth@sha256:2098e1331c3499781791582076491b38545e27140c5a28b5c476a08b9b5c5f6f"

const mfaLocalImage = "remnamanager/mfa-gateway:caddy2.9.1-auth1.1.27"
const mfaDockerfile = `FROM --platform=$BUILDPLATFORM caddy:2.9.1-builder AS builder
ARG TARGETOS=linux
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH xcaddy build v2.9.1 --with github.com/greenpau/caddy-security@v1.1.27
FROM caddy:2.9.1
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`

func mfaContainerImage() string {
	if runtime.GOARCH == "arm64" {
		return mfaLocalImage
	}
	return mfaImage
}

func renderMFAEnv(s State) []byte {
	return []byte(fmt.Sprintf("AUTHP_ADMIN_USER=%s\nAUTHP_ADMIN_EMAIL=%s@%s\nAUTHP_ADMIN_SECRET=%s\nMFA_SIGNING_KEY=%s\n", s.Params.Admin, s.Params.Admin, s.Params.PanelDomain, s.MFAPassword, s.MFASigningKey))
}

// RenderMFAGateway is an internal HTTP gateway. HTTPS terminates at the selected
// Nginx/Caddy front proxy. Only the portal routes are public; every application
// route, including /api, requires a portal session or a portal API key.
func RenderMFAGateway(s State) string {
	return fmt.Sprintf(`{
 admin off
 auto_https off
 order authenticate before respond
 order authorize before respond
 security {
  local identity store localdb {
   realm local
   path /data/users.json
  }
  authentication portal remnamanager {
   crypto default token lifetime 604800
   crypto key sign-verify {$MFA_SIGNING_KEY}
   enable identity store localdb
   cookie domain %s
   cookie insecure off
   ui {
    links {
     "Remnawave" "/auth/login"
     "API Keys" "/r/settings/apikeys"
     "MFA" "/r/settings/mfa"
    }
   }
   transform user {
    match origin local
    require mfa
   }
  }
  authorization policy panelpolicy {
   set auth url https://%s/r
   crypto key verify {$MFA_SIGNING_KEY}
   allow roles authp/admin
   with api key auth portal remnamanager realm local
  }
 }
}

http://:8080 {
 header Referrer-Policy no-referrer
 handle /_rm_gateway_health {
  respond 200
 }
 handle /r {
  rewrite * /auth
  request_header X-Forwarded-Prefix /r
  authenticate with remnamanager
 }
 handle /r/* {
  authenticate with remnamanager
 }
 handle {
  route {
   authorize with panelpolicy
   reverse_proxy remnawave:3000 {
    header_up Host %s
    header_up X-Forwarded-Host %s
    header_up X-Forwarded-Proto https
    header_up -X-Api-Key
    header_up -Remote-User
    header_up -Remote-Email
    header_up -Remote-Groups
   }
  }
 }
}
`, s.Params.PanelDomain, s.Params.PanelDomain, s.Params.PanelDomain, s.Params.PanelDomain)
}

func (r *Real) backupMFAUsers(ctx context.Context, directory string) error {
	stream, ok := r.Runner.(StreamRunner)
	if !ok {
		return errors.New("runner does not support streaming MFA backups")
	}
	path := filepath.Join(directory, "mfa-users.json.partial")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = stream.RunTo(ctx, f, "docker", r.composeArgs("exec", "-T", "mfa-gateway", "cat", "/data/users.json")...)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return errors.New("invalid MFA identity backup; partial file retained")
	}
	return os.Rename(path, filepath.Join(directory, "mfa-users.json"))
}
