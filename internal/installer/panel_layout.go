package installer

import (
	"context"
	"encoding/json"
	"path/filepath"
)

func (r *Real) stackComposePath() string {
	name := "compose.json"
	if r.state.LayoutVersion >= 1 {
		name = "docker-compose.yml"
	}
	return filepath.Join(r.Stack, name)
}

func (r *Real) subComposePath() string {
	if r.state.LayoutVersion >= 1 {
		return filepath.Join(r.Stack, "subscription", "docker-compose.yml")
	}
	return r.stackComposePath()
}

func (r *Real) subEnvPath() string {
	if r.state.LayoutVersion >= 1 {
		return filepath.Join(r.Stack, "subscription", ".env")
	}
	return filepath.Join(r.Stack, "subscription.env")
}

func subscriptionCommand(args []string) bool {
	found := false
	for i, arg := range args {
		if i > 0 && args[i-1] == "--profile" {
			continue
		}
		switch arg {
		case "remnawave", "remnawave-db", "remnawave-redis", "tinyauth", "mfa-gateway":
			return false
		case "subscription":
			found = true
		}
	}
	return found
}

func (r *Real) subscriptionCompose(ctx context.Context, args ...string) ([]byte, error) {
	return r.Runner.Run(ctx, nil, "docker", append([]string{"compose", "--project-directory", filepath.Join(r.Stack, "subscription"), "-f", r.subComposePath()}, officialArgs(r.state, args)...)...)
}

func RenderLocalSubscription(s State) ([]byte, error) {
	if s.LayoutVersion >= officialLayout {
		return patchComposeImage([]byte(templateFile("subscription.compose.yml")), "remnawave-subscription-page", s.SubImage)
	}
	s.LayoutVersion = 0
	s.Params.RemoteSubscription = nil
	data, err := RenderStack(s)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	sub := doc["services"].(map[string]any)["subscription"].(map[string]any)
	sub["env_file"] = []string{".env"}
	delete(sub, "profiles")
	return json.MarshalIndent(map[string]any{"name": "remnamanager-subscription-local", "services": map[string]any{"subscription": sub}, "networks": map[string]any{"panel": map[string]any{"external": true, "name": "remnamanager-panel_panel"}}}, "", "  ")
}
