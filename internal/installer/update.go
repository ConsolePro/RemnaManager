package installer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./:_-]*(?:@sha256:[a-f0-9]{64})?$`)

func ValidateBackendVersion(value string) error {
	if !versionPattern.MatchString(value) {
		return errors.New("specify a stable backend version such as 3.4.9; latest and prereleases are not accepted")
	}
	return nil
}
func ValidateSubscriptionImage(value string) error {
	if len(value) > 255 || !imagePattern.MatchString(value) {
		return errors.New("invalid Docker image reference")
	}
	return nil
}
func versionOlder(target, current string) bool {
	a, b := strings.Split(target, "."), strings.Split(strings.TrimPrefix(current, "v"), ".")
	if len(a) != 3 || len(b) != 3 {
		return true
	}
	for i := range a {
		x, e1 := strconv.Atoi(a[i])
		y, e2 := strconv.Atoi(b[i])
		if e1 != nil || e2 != nil {
			return true
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func (r *Real) updatePanel(ctx context.Context, version string) (string, error) {
	if err := ValidateBackendVersion(version); err != nil {
		return "", err
	}
	if versionOlder(version, r.state.BackendTag) {
		return "", errors.New("backend downgrades require an explicit database restore and are not supported by Update")
	}
	if version == r.state.BackendTag {
		return "Backend is already at " + version + "; no changes made.", nil
	}
	// Check an official stable release, not arbitrary Docker tags or user URLs.
	request, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/remnawave/backend/releases/tags/"+version, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	resp, err := r.Client.Do(request)
	if err != nil {
		return "", errors.New("cannot verify official backend release")
	}
	var release struct {
		Tag               string `json:"tag_name"`
		Draft, Prerelease bool
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release)
	resp.Body.Close()
	if resp.StatusCode != 200 || decodeErr != nil || release.Tag != version || release.Draft || release.Prerelease {
		return "", errors.New("official stable release was not found; no changes made")
	}
	// Download before downtime, then quiesce API writes for the database snapshot.
	if _, err = r.Runner.Run(ctx, nil, "docker", "pull", "remnawave/backend:"+version); err != nil {
		return "", err
	}
	if _, err = r.compose(ctx, "stop", "remnawave"); err != nil {
		return "", err
	}
	resumeOld := func(cause error) error {
		resume, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, restartErr := r.compose(resume, "up", "-d", "--no-deps", "remnawave")
		return errors.Join(cause, restartErr)
	}
	backup, err := r.backup(ctx)
	if err != nil {
		return "", resumeOld(err)
	}
	old, err := r.setComposeImage("remnawave", "remnawave/backend:"+version)
	if err != nil {
		return "", resumeOld(err)
	}
	restore := func() error { return writeFile(filepath.Join(r.Stack, "compose.json"), old, 0600) }
	_, err = r.compose(ctx, "config", "--quiet")
	if err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return "", errors.Join(err, restoreErr)
		}
		return "", resumeOld(err)
	}
	r.state.RecoveryBackup = backup
	if err = saveState(statePath(r.Root), r.state); err != nil {
		r.state.RecoveryBackup = ""
		if restoreErr := restore(); restoreErr != nil {
			return "", errors.Join(err, restoreErr)
		}
		return "", resumeOld(err)
	}
	if _, err = r.compose(ctx, "up", "-d", "--no-deps", "--wait", "--wait-timeout", "240", "remnawave"); err == nil {
		err = r.waitAPI(ctx)
	}
	if err != nil {
		// Never start an old backend against a possibly migrated database.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, stopErr := r.compose(cleanup, "stop", "remnawave")
		return "", errors.Join(fmt.Errorf("backend update failed; automatic database downgrade is unsafe; backup: %s: %w", backup, err), stopErr, restore())
	}
	r.state.BackendTag = version
	r.state.RecoveryBackup = ""
	if err = saveState(statePath(r.Root), r.state); err != nil {
		return "", fmt.Errorf("updated backend is running but state was not saved; inspect recovery record and backup %s: %w", backup, err)
	}
	return "Backend updated to " + version + ".\nDatabase and configuration backup: " + backup, nil
}

func subscriptionUpdateSource(image string) string {
	if at := strings.Index(image, "@"); at >= 0 {
		return image[:at] + ":latest"
	}
	return image
}
func imageRepository(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	if at := strings.IndexByte(image, '@'); at >= 0 {
		return image[:at]
	}
	if at := strings.LastIndexByte(image, ':'); at > strings.LastIndexByte(image, '/') {
		return image[:at]
	}
	return image
}
func (r *Real) resolveImage(ctx context.Context, source string) (string, error) {
	if err := ValidateSubscriptionImage(source); err != nil {
		return "", err
	}
	if _, err := r.Runner.Run(ctx, nil, "docker", "pull", source); err != nil {
		return "", err
	}
	data, err := r.Runner.Run(ctx, nil, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", source)
	if err != nil {
		return "", err
	}
	digest := strings.TrimPrefix(strings.TrimSpace(string(data)), "docker.io/")
	parts := strings.Split(digest, "@sha256:")
	if len(parts) != 2 || parts[0] != imageRepository(source) {
		return "", errors.New("unexpected image digest repository")
	}
	b, err := hex.DecodeString(parts[1])
	if err != nil || len(b) != 32 {
		return "", errors.New("invalid image digest")
	}
	return digest, nil
}
func (r *Real) updateSubscription(ctx context.Context, source string) (string, error) {
	if source == "" {
		source = r.state.SubSource
		if source == "" {
			source = subscriptionUpdateSource(r.state.SubImage)
		}
	}
	digest, err := r.resolveImage(ctx, source)
	if err != nil {
		return "", err
	}
	oldState := r.state
	old, err := os.ReadFile(filepath.Join(r.Stack, "compose.json"))
	if err != nil {
		return "", err
	}
	recoveryPath := filepath.Join(r.Root, "subscription-before-update.json")
	if err = writeFile(recoveryPath, old, 0600); err != nil {
		return "", err
	}
	r.state.SubRecovery = recoveryPath
	if err = saveState(statePath(r.Root), r.state); err != nil {
		r.state = oldState
		return "", err
	}
	_, err = r.setComposeImage("subscription", digest)
	if err != nil {
		r.state = oldState
		return "", errors.Join(err, saveState(statePath(r.Root), r.state))
	}
	if _, err = r.compose(ctx, "--profile", "subscription", "up", "-d", "--no-deps", "subscription"); err == nil {
		err = r.waitSubscription(ctx)
	}
	if err == nil {
		r.state.SubImage = digest
		r.state.SubSource = source
		r.state.SubRecovery = ""
		err = saveState(statePath(r.Root), r.state)
	}
	if err != nil {
		r.state = oldState
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restoreErr := writeFile(filepath.Join(r.Stack, "compose.json"), old, 0600)
		if restoreErr == nil {
			_, restoreErr = r.compose(recovery, "--profile", "subscription", "up", "-d", "--no-deps", "subscription")
		}
		if restoreErr != nil {
			r.state.SubRecovery = recoveryPath
		}
		return "", errors.Join(err, restoreErr, saveState(statePath(r.Root), r.state))
	}
	return "Subscription image updated and checked:\n" + digest, nil
}
func (r *Real) startSubscription(ctx context.Context) error {
	if r.state.SubRecovery != "" {
		expected := filepath.Join(r.Root, "subscription-before-update.json")
		if r.state.SubRecovery != expected {
			return errors.New("invalid subscription recovery path")
		}
		old, err := os.ReadFile(expected)
		if err != nil {
			return err
		}
		if err = writeFile(filepath.Join(r.Stack, "compose.json"), old, 0600); err != nil {
			return err
		}
	}
	if _, err := r.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240", "remnawave"); err != nil {
		return err
	}
	if err := r.bootstrap(ctx); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(r.Stack, "subscription.env"), renderSubEnv(r.state), 0600); err != nil {
		return err
	}
	if _, err := r.compose(ctx, "--profile", "subscription", "up", "-d", "subscription"); err != nil {
		return err
	}
	if err := r.waitSubscription(ctx); err != nil {
		return err
	}
	r.state.SubRecovery = ""
	return saveState(statePath(r.Root), r.state)
}
func (r *Real) waitSubscription(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		id, err := r.compose(wait, "--profile", "subscription", "ps", "-q", "subscription")
		if err == nil && strings.TrimSpace(string(id)) != "" {
			status, e := r.Runner.Run(wait, nil, "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}missing-healthcheck{{end}}", strings.TrimSpace(string(id)))
			if e == nil {
				switch strings.TrimSpace(string(status)) {
				case "healthy":
					return nil
				case "missing-healthcheck":
					return errors.New("subscription image must provide a Docker HEALTHCHECK; root / is not a public health endpoint")
				}
			}
		}
		select {
		case <-wait.Done():
			return errors.New("subscription page did not become ready")
		case <-time.After(time.Second):
		}
	}
}
