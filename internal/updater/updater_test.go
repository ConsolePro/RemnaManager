package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestClosedCacheCannotRecreateConfigurationAfterRemoval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"tag_name":"v1.3.1"}`)) }))
	defer server.Close()
	control := &CacheControl{}
	checker := Checker{URL: server.URL, Client: server.Client(), Cache: filepath.Join(t.TempDir(), "removed-config", "update-cache.json"), CacheControl: control}
	copyOfChecker := checker
	control.Close()
	if _, err := copyOfChecker.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(checker.Cache)); !os.IsNotExist(err) {
		t.Fatal("background check recreated removed configuration", err)
	}
}

func TestCheckCacheAndOffline(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"tag_name":"v1.2.0"}`)) }))
	checker := Checker{URL: server.URL, Client: server.Client(), Cache: filepath.Join(t.TempDir(), "cache.json")}
	r, e := checker.Check(context.Background())
	if e != nil || r.Version != "v1.2.0" {
		t.Fatal(r, e)
	}
	server.Close()
	r, e = checker.Check(context.Background())
	if e != nil || r.Version != "v1.2.0" || calls != 1 {
		t.Fatal(r, e, calls)
	}
	checker.Cache = ""
	if _, e = checker.Check(context.Background()); e == nil {
		t.Fatal("expected offline error")
	}
}
func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"v1.10.0", "v1.9.9", true}, {"v1.0.0", "v1.0.0", false}, {"v0.99.0", "v1.0.0", false}, {"v2.0.0-beta", "v1.0.0", false}, {"garbage", "v1.0.0", false},
	} {
		if Newer(tt.a, tt.b) != tt.want {
			t.Fatal(tt)
		}
	}
}
func TestHTTPFailures(t *testing.T) {
	for _, body := range []string{"bad JSON", `{"tag_name":"v2.0.0","prerelease":true}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		r, e := (Checker{URL: server.URL}).Check(context.Background())
		if body == "bad JSON" && e == nil || body != "bad JSON" && r.Version != "" {
			t.Fatal(r, e)
		}
		server.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Checker{}).Check(ctx); e == nil {
		t.Fatal("cancellation ignored")
	}
}
