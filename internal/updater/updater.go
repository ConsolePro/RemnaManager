package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var Version = "v1.3.0"

const Endpoint = "https://api.github.com/repos/ConsolePro/RemnaManager/releases/latest"

type Result struct {
	Version    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Checked    time.Time `json:"checked"`
}

type Checker struct {
	Client     *http.Client
	URL, Cache string
}

func (c Checker) Check(ctx context.Context) (Result, error) {
	var r Result
	if data, e := os.ReadFile(c.Cache); e == nil && json.Unmarshal(data, &r) == nil && time.Since(r.Checked) >= 0 && time.Since(r.Checked) < 15*time.Minute {
		return r, nil
	}
	url := c.URL
	if url == "" {
		url = Endpoint
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "RemnaManager-Go-preview")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GitHub: HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Result{}, err
	}
	if r.Draft || r.Prerelease {
		r.Version = ""
	}
	r.Checked = time.Now()
	if c.Cache != "" {
		// Cache failures never prevent starting the application.
		if os.MkdirAll(filepath.Dir(c.Cache), 0700) == nil {
			if f, e := os.CreateTemp(filepath.Dir(c.Cache), ".update-*"); e == nil {
				name := f.Name()
				e = json.NewEncoder(f).Encode(r)
				closeErr := f.Close()
				if e == nil && closeErr == nil {
					_ = os.Rename(name, c.Cache)
				}
				_ = os.Remove(name)
			}
		}
	}
	return r, nil
}

func Newer(candidate, current string) bool {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
		if len(parts) != 3 {
			return v, false
		}
		for i, p := range parts {
			n, e := strconv.Atoi(p)
			if e != nil || n < 0 {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	a, ok := parse(candidate)
	if !ok {
		return false
	}
	b, ok := parse(current)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
