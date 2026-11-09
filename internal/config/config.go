package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
)

const DefaultPath = "/etc/remnamanager/config.json"

type Config struct {
	Language    string `json:"language"`
	Email       string `json:"email"`
	Initialized bool   `json:"initialized"`
}

func ValidEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && !strings.ContainsAny(s, "\r\n") && strings.Contains(strings.SplitN(s, "@", 2)[1], ".")
}

func (c Config) Validate() error {
	if c.Language != "ru" && c.Language != "en" {
		return errors.New("language must be ru or en")
	}
	if !ValidEmail(c.Email) {
		return errors.New("invalid administrator email")
	}
	return nil
}

type Store struct{ Path string }

func (s Store) Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	if strings.TrimSpace(string(data)) == "null" {
		return c, errors.New("config must be a JSON object")
	}
	if c.Initialized {
		err = c.Validate()
	}
	return c, err
}

// Save preserves unknown keys so future settings survive an edit by this version.
func (s Store) Save(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(s.Path)
	if err == nil {
		if err = json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if fields == nil {
			return errors.New("config must be a JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encoded, _ := json.Marshal(c)
	var known map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &known)
	for k, v := range known {
		fields[k] = v
	}
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
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
	return os.Rename(f.Name(), s.Path)
}
