package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

// Maintainer-only fixture generation. The installer itself reads the resulting
// templates and substitutes values; it does not rebuild these configurations.
func TestWriteOfficialProxyTemplates(t *testing.T) {
	root := os.Getenv("RM_WRITE_PROXY_TEMPLATES")
	if root == "" {
		t.Skip("maintainer template fixture")
	}
	for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
		for _, bundled := range []bool{true, false} {
			p := testParams()
			p.PanelDomain = "panel.example.test"
			p.SubscriptionDomain = "sub.example.test"
			p.Protection = mode
			if mode == "tinyauth" {
				p.AuthDomain = "auth.panel.example.test"
			}
			s, err := newState(p)
			if err != nil {
				t.Fatal(err)
			}
			s.SubToken = "fixture-scoped-token"
			suffix := "bundled"
			if !bundled {
				suffix = "separate"
				s.Params.RemoteSubscription = &services.SubscriptionRemote{User: "root", Proxy: "caddy"}
			}
			for _, proxy := range []string{"nginx", "caddy"} {
				s.Params.Proxy = proxy
				text := dockerProxyConfig(s, true)
				text = strings.ReplaceAll(text, "subscription:3010", "remnawave-subscription-page:3010")
				for _, domain := range proxyDomains(s.Params) {
					text = strings.ReplaceAll(text, "/etc/nginx/certs/"+domain+"/fullchain.pem", "/etc/nginx/ssl/fullchain.pem")
					text = strings.ReplaceAll(text, "/etc/nginx/certs/"+domain+"/privkey.pem", "/etc/nginx/ssl/privkey.key")
				}
				replacements := []string{p.PanelDomain, "REPLACE_WITH_YOUR_DOMAIN", p.SubscriptionDomain, "SUBSCRIPTION_PAGE_DOMAIN", p.Email, "ACME_EMAIL", s.SubToken, "SUBSCRIPTION_API_TOKEN"}
				if s.CookieKey != "" {
					replacements = append(replacements, s.CookieKey, "COOKIE_NAME", s.CookieValue, "COOKIE_SECRET")
				}
				text = strings.NewReplacer(replacements...).Replace(text)
				// Auth domain was replaced as a suffix of panel domain above.
				text = strings.ReplaceAll(text, "auth.REPLACE_WITH_YOUR_DOMAIN", "AUTH_DOMAIN")
				if err = os.WriteFile(filepath.Join(root, proxy+"-"+mode+"-"+suffix+".conf"), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
