package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var TagPattern = regexp.MustCompile(`^v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}$`)
var SHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type Release struct {
	Version    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type Catalog struct {
	Client *http.Client
	Repo   string
}

func (c Catalog) Get(ctx context.Context, tag string) (Release, error) {
	if c.Repo != "ConsolePro/RemnaManager" && c.Repo != "XTLS/Xray-core" {
		return Release{}, errors.New("unsupported release repository")
	}
	path := "latest"
	if tag != "" && tag != "latest" {
		if !TagPattern.MatchString(tag) {
			return Release{}, errors.New("version must have the form v1.2.3")
		}
		path = "tags/" + tag
	}
	data, err := c.Read(ctx, "https://api.github.com/repos/"+c.Repo+"/releases/"+path, 4<<20)
	if err != nil {
		return Release{}, err
	}
	var release Release
	if err = json.Unmarshal(data, &release); err != nil {
		return release, err
	}
	if release.Draft || release.Prerelease || !TagPattern.MatchString(release.Version) || path != "latest" && release.Version != tag {
		return Release{}, errors.New("expected a stable release with a matching version")
	}
	return release, nil
}

func (c Catalog) Read(ctx context.Context, address string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RemnaManager")
	req.Header.Set("Accept", "application/vnd.github+json")
	if req.URL.Host == "github.com" {
		req.Header.Set("Accept", "application/octet-stream")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	safe := *client
	safe.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		if req.URL.Scheme != "https" || len(via) > 8 || (host != "github.com" && host != "api.github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
			return errors.New("unsafe release redirect")
		}
		return nil
	}
	response, err := safe.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release file exceeds size limit")
	}
	return data, nil
}

func (c Catalog) Asset(release Release, name string) (Asset, error) {
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		parsed, err := url.Parse(asset.URL)
		prefix := "/" + c.Repo + "/releases/download/" + release.Version + "/"
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != prefix+name {
			return Asset{}, errors.New("unexpected release asset URL")
		}
		return asset, nil
	}
	return Asset{}, fmt.Errorf("release %s does not include %s", release.Version, name)
}

func (c Catalog) Digest(ctx context.Context, release Release, asset Asset) (string, error) {
	if strings.HasPrefix(asset.Digest, "sha256:") {
		sum := strings.TrimPrefix(asset.Digest, "sha256:")
		if SHA256Pattern.MatchString(sum) {
			return sum, nil
		}
		return "", errors.New("invalid release SHA256")
	}
	if c.Repo == "XTLS/Xray-core" {
		checksum, err := c.Asset(release, asset.Name+".dgst")
		if err != nil {
			return "", err
		}
		data, err := c.Read(ctx, checksum.URL, 64<<10)
		if err != nil {
			return "", err
		}
		match := regexp.MustCompile(`(?im)^SHA(?:2-)?256\s*(?:\([^\n]*\))?\s*=\s*([a-f0-9]{64})\s*$`).FindSubmatch(data)
		if len(match) != 2 {
			return "", errors.New("missing Xray SHA256")
		}
		return strings.ToLower(string(match[1])), nil
	}
	checksum, err := c.Asset(release, "checksums.txt")
	if err != nil {
		return "", err
	}
	data, err := c.Read(ctx, checksum.URL, 64<<10)
	if err != nil {
		return "", err
	}
	found := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset.Name {
			if found != "" || !SHA256Pattern.MatchString(fields[0]) {
				return "", errors.New("invalid or duplicate checksum")
			}
			found = fields[0]
		}
	}
	if found == "" {
		return "", errors.New("missing release checksum")
	}
	return found, nil
}
