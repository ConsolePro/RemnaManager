package installer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderStack deliberately publishes neither the database nor Valkey/metrics.
// Host reverse proxies can reach only the two loopback application endpoints.
func RenderStack(s State) ([]byte, error) {
	common := func(image string) map[string]any {
		return map[string]any{"image": image, "restart": "unless-stopped", "networks": []string{"panel"}, "logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}}}
	}
	backend := common("remnawave/backend:" + s.BackendTag)
	backend["env_file"] = []string{".env"}
	backend["ports"] = []string{"127.0.0.1:3000:3000"}
	backend["volumes"] = []string{"valkey-socket:/var/run/valkey"}
	backend["depends_on"] = map[string]any{"remnawave-db": map[string]string{"condition": "service_healthy"}, "remnawave-redis": map[string]string{"condition": "service_healthy"}}
	backend["healthcheck"] = map[string]any{"test": []string{"CMD", "curl", "-fsS", "http://localhost:3001/health"}, "interval": "10s", "timeout": "5s", "retries": 12, "start_period": "30s"}
	database := common("postgres:18.4")
	database["environment"] = map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_DB": "postgres", "POSTGRES_PASSWORD": s.DBPassword, "TZ": "UTC"}
	database["volumes"] = []string{"database:/var/lib/postgresql"}
	database["shm_size"] = "256mb"
	database["healthcheck"] = map[string]any{"test": []string{"CMD", "pg_isready", "-U", "postgres", "-d", "postgres"}, "interval": "3s", "timeout": "5s", "retries": 20}
	cache := common("valkey/valkey:9-alpine")
	cache["volumes"] = []string{"valkey-socket:/var/run/valkey"}
	cache["command"] = []string{"valkey-server", "--save", "", "--appendonly", "no", "--maxmemory-policy", "noeviction", "--unixsocket", "/var/run/valkey/valkey.sock", "--unixsocketperm", "777", "--port", "0"}
	cache["healthcheck"] = map[string]any{"test": []string{"CMD", "valkey-cli", "-s", "/var/run/valkey/valkey.sock", "ping"}, "interval": "3s", "timeout": "3s", "retries": 20}
	sub := common(s.SubImage)
	sub["env_file"] = []string{"subscription.env"}
	sub["ports"] = []string{"127.0.0.1:3010:3010"}
	sub["profiles"] = []string{"subscription"}
	return json.MarshalIndent(map[string]any{"name": "remnamanager-panel", "services": map[string]any{"remnawave": backend, "remnawave-db": database, "remnawave-redis": cache, "subscription": sub}, "networks": map[string]any{"panel": map[string]string{"driver": "bridge"}}, "volumes": map[string]any{"database": map[string]any{}, "valkey-socket": map[string]any{}}}, "", "  ")
}

func renderEnv(s State) []byte {
	values := []string{"APP_PORT=3000", "METRICS_PORT=3001", "API_INSTANCES=1", "DATABASE_URL=postgresql://postgres:" + s.DBPassword + "@remnawave-db:5432/postgres", "POSTGRES_USER=postgres", "POSTGRES_DB=postgres", "POSTGRES_PASSWORD=" + s.DBPassword, "REDIS_SOCKET=/var/run/valkey/valkey.sock", "APP_SECRET=" + s.AppSecret, "METRICS_USER=admin", "METRICS_PASS=" + s.MetricsPassword, "WEBHOOK_SECRET_HEADER=" + s.WebhookSecret, "WEBHOOK_ENABLED=false", "IS_TELEGRAM_NOTIFICATIONS_ENABLED=false", "EXPORT_TO_STREAM_ENABLED=false", "PANEL_DOMAIN=" + s.Params.PanelDomain, "FRONT_END_DOMAIN=" + s.Params.PanelDomain, "SUB_PUBLIC_DOMAIN=" + s.Params.SubscriptionDomain, "SERVICE_SNI_VERIFICATION=true"}
	return []byte(strings.Join(values, "\n") + "\n")
}
func renderSubEnv(s State) []byte {
	return []byte(fmt.Sprintf("APP_PORT=3010\nREMNAWAVE_PANEL_URL=http://remnawave:3000\nREMNAWAVE_API_TOKEN=%s\nTRUST_PROXY=1\n", s.SubToken))
}
