package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const panelTemplateVersion = "3.4.9"

var panelArtifactHashes = map[string]string{
	"docker-compose-prod.yml": "4f9ca29cbb19c389a1cd6c10e378259dd2ec6bab8e7e06b78851207c2395b8a5",
	".env.sample":             "45be9ce2071db59a50321994607bcb22280425572d0b6c7fb68f8878cbcbb2b7",
}

func artifactHashValid(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == want
}
func (r *Real) downloadPanelArtifacts(ctx context.Context) error {
	if r.state.TemplateVersion != "" && r.state.TemplateVersion != panelTemplateVersion {
		return errors.New("unsupported official template version; no fallback will be generated")
	}
	for name, want := range panelArtifactHashes {
		source := "https://raw.githubusercontent.com/remnawave/backend/" + panelTemplateVersion + "/" + name
		path := filepath.Join(r.Root, "templates", panelTemplateVersion, name)
		if data, err := os.ReadFile(path); err == nil {
			if !artifactHashValid(data, want) {
				return fmt.Errorf("cached official artifact checksum mismatch: %s", name)
			}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
			if err != nil {
				return err
			}
			response, err := r.Client.Do(req)
			if err != nil {
				return fmt.Errorf("cannot download official %s: %w", name, err)
			}
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if response.StatusCode != http.StatusOK || readErr != nil {
				return fmt.Errorf("official artifact download failed: %s (HTTP %d)", name, response.StatusCode)
			}
			if response.Request != nil && response.Request.URL.String() != source {
				return errors.New("official artifact source redirected; refusing source substitution")
			}
			if !artifactHashValid(data, want) {
				return fmt.Errorf("official artifact checksum mismatch: %s; no generated substitute", name)
			}
			if err = writeFile(path, data, 0600); err != nil {
				return err
			}
		}
		if r.log != nil {
			fmt.Fprintf(r.log, "Official template: backend=%s template=%s source=%s sha256=%s verified\n", r.state.BackendTag, panelTemplateVersion, source, want)
		}
	}
	r.state.TemplateVersion = panelTemplateVersion
	return saveState(statePath(r.Root), r.state)
}
