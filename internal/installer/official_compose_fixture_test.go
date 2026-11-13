package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOfficialComposeFixtures(t *testing.T) {
	root := os.Getenv("RM_OFFICIAL_COMPOSE_FIXTURE")
	if root == "" {
		t.Skip("opt-in Compose schema fixture")
	}
	for _, mode := range []string{"cookie", "tinyauth", "mfa"} {
		r := NewReal()
		r.Stack = filepath.Join(root, mode)
		p := testParams()
		p.Protection = mode
		if mode == "tinyauth" {
			p.AuthDomain = "auth." + p.PanelDomain
		}
		var err error
		r.state, err = newState(p)
		if err != nil {
			t.Fatal(err)
		}
		r.state.LayoutVersion = officialLayout
		r.state.BackendTag = "3.4.9"
		if err = r.configure(); err != nil {
			t.Fatal(err)
		}
		for _, proxy := range []string{"nginx", "caddy"} {
			r.state.Params.Proxy = proxy
			if err = r.writeOfficialProxy(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
