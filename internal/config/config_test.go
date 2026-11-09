package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "nested", "config.json")}
	c, e := s.Load()
	if e != nil || c.Initialized {
		t.Fatal(c, e)
	}
	want := Config{Language: "ru", Email: "admin@example.com", Initialized: true}
	if e = s.Save(want); e != nil {
		t.Fatal(e)
	}
	got, e := s.Load()
	if e != nil || got != want {
		t.Fatal(got, e)
	}
	data, _ := os.ReadFile(s.Path)
	data = []byte(strings.Replace(string(data), "{", "{\"future_setting\":42,", 1))
	if e = os.WriteFile(s.Path, data, 0600); e != nil {
		t.Fatal(e)
	}
	want.Language = "en"
	if e = s.Save(want); e != nil {
		t.Fatal(e)
	}
	data, _ = os.ReadFile(s.Path)
	if !strings.Contains(string(data), "future_setting") {
		t.Fatal("unknown setting lost")
	}
	before := string(data)
	bad := want
	bad.Email = "invalid"
	if s.Save(bad) == nil {
		t.Fatal("invalid email accepted")
	}
	data, _ = os.ReadFile(s.Path)
	if string(data) != before {
		t.Fatal("failed save modified config")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(s.Path)
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		info, _ = os.Stat(filepath.Dir(s.Path))
		if info.Mode().Perm() != 0700 {
			t.Fatal(info.Mode())
		}
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.Path), ".config-*"))
	if len(files) != 0 {
		t.Fatal(files)
	}
}
func TestInvalidConfigIsNotOverwritten(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "config.json")}
	_ = os.WriteFile(s.Path, []byte("broken"), 0600)
	if _, e := s.Load(); e == nil {
		t.Fatal("corrupt config accepted")
	}
	if s.Save(Config{Language: "en", Email: "a@b.com", Initialized: true}) == nil {
		t.Fatal("corrupt config overwritten")
	}
	_ = os.WriteFile(s.Path, []byte("null"), 0600)
	if _, e := s.Load(); e == nil {
		t.Fatal("null config accepted")
	}
	if s.Save(Config{Language: "en", Email: "a@b.com", Initialized: true}) == nil {
		t.Fatal("null config overwritten")
	}
}
func TestEmail(t *testing.T) {
	for _, s := range []string{"a@example.com", "user+tag@example.org"} {
		if !ValidEmail(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "Name <a@example.com>", "a@b", "a@example.com\n", "a@@example.com"} {
		if ValidEmail(s) {
			t.Fatal(s)
		}
	}
}
