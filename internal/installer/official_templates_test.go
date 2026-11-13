package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOfficialComposeTemplatesAndNames(t *testing.T) {
	s, _ := newState(testParams())
	s.LayoutVersion = officialLayout
	s.BackendTag = "3.4.9"
	data, err := RenderStack(s)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		t.Fatal("JSON emitted as YAML")
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("container_name: remnawave")) || !bytes.Contains(data, []byte("name: remnawave-network")) {
		t.Fatal("official identities changed")
	}
	sub, err := RenderLocalSubscription(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sub, []byte("remnawave-subscription-page:")) {
		t.Fatal("nonstandard subscription service")
	}
	for _, proxy := range []string{"nginx", "caddy"} {
		s.Params.Proxy = proxy
		for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
			s.Params.Protection = mode
			if mode == "tinyauth" {
				s.Params.AuthDomain = "auth." + s.Params.PanelDomain
			} else {
				s.Params.AuthDomain = ""
			}
			if err = ensureProtection(&s); err != nil {
				t.Fatal(err)
			}
			config := dockerProxyConfig(s, true)
			for _, placeholder := range []string{"REPLACE_WITH_YOUR_DOMAIN", "SUBSCRIPTION_PAGE_DOMAIN", "AUTH_DOMAIN", "COOKIE_NAME", "COOKIE_SECRET", "ACME_EMAIL"} {
				if strings.Contains(config, placeholder) {
					t.Fatal("unexpanded placeholder", proxy, mode, placeholder)
				}
			}
			if proxy == "nginx" && !strings.Contains(config, "/etc/nginx/ssl/fullchain.pem") {
				t.Fatal("shared certificate not used")
			}
		}
	}
}

func TestYAMLImageUpdatePreservesEveryOtherByte(t *testing.T) {
	before := []byte("# user comment\nservices:\n  remnawave:\n    image: 'remnawave/backend:3.4.8' # pinned\n    environment:\n      CUSTOM: keep-me\nnetworks: {}\n")
	after, err := patchComposeImage(before, "remnawave", "remnawave/backend:3.4.9")
	if err != nil {
		t.Fatal(err)
	}
	expected := bytes.Replace(before, []byte("'remnawave/backend:3.4.8'"), []byte("remnawave/backend:3.4.9"), 1)
	if !bytes.Equal(after, expected) {
		t.Fatalf("unrelated YAML changed:\n%s", after)
	}
}

func TestOfficialServiceAliasesDoNotRenameExecutable(t *testing.T) {
	s := State{LayoutVersion: officialLayout}
	for _, args := range [][]string{{"exec", "-T", "nginx", "nginx", "-s", "reload"}, {"run", "--rm", "--no-deps", "nginx", "nginx", "-t"}} {
		expected := append([]string(nil), args...)
		for i, name := range expected {
			if name == "nginx" {
				expected[i] = "remnawave-nginx"
				break
			}
		}
		if got := officialArgs(s, args); !reflect.DeepEqual(got, expected) {
			t.Fatal("renamed executable", got)
		}
	}
}

func TestOfficialConfigurePreservesUserComposeAndEnv(t *testing.T) {
	r, _ := managementFixture(t)
	r.Stack = t.TempDir()
	r.state.LayoutVersion = officialLayout
	if err := r.configure(); err != nil {
		t.Fatal(err)
	}
	file := r.stackComposePath()
	original, _ := os.ReadFile(file)
	custom := append(original, []byte("\n# custom setting preserved\n")...)
	os.WriteFile(file, custom, 0600)
	env := []byte("CUSTOM=keep\nAPP_PORT=3000\n")
	os.WriteFile(filepath.Join(r.Stack, ".env"), env, 0600)
	if err := r.configure(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, custom) {
		t.Fatal("Compose overwritten")
	}
	got, _ = os.ReadFile(filepath.Join(r.Stack, ".env"))
	if !bytes.Equal(got, env) {
		t.Fatal("env overwritten")
	}
}
