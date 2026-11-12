package installer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func quickPreview(t *testing.T, r *Real) string {
	t.Helper()
	result, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-preview", Spec: services.RoutingSpec{BaseTemplateUUID: routeBaseID}, Nodes: []string{routeNodeID}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Scheme.ID
}

func TestQuickRoutingUpdatesExistingTemplateWithoutCreatingResources(t *testing.T) {
	r, f := routingFixture(t)
	id := quickPreview(t, r)
	if len(f.writes) != 0 {
		t.Fatal("preview wrote to API")
	}
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-apply", ID: id}); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || f.patches != 1 || len(f.writes) != 1 || f.writes[0] != "PATCH /subscription-templates" {
		t.Fatal("quick flow created a template, host or squad", f.writes)
	}
	if !strings.Contains(string(f.baseConfig), "geoip:ru") || !strings.Contains(string(f.baseConfig), routeHostID) {
		t.Fatal("node did not resolve to its subscription host")
	}
	second := quickPreview(t, r)
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-apply", ID: second}); err != nil {
		t.Fatal(err)
	}
	if f.patches != 1 {
		t.Fatal("unchanged config was patched again")
	}
	j, err := r.loadQuickRouting(second)
	if err != nil || jsonHash(j.Before) != jsonHash(json.RawMessage(routeTestConfig)) {
		t.Fatal("original backup was lost", err)
	}
}

func TestQuickRoutingRecoversLostPatchAndProtectsManualChanges(t *testing.T) {
	r, f := routingFixture(t)
	id := quickPreview(t, r)
	f.losePatch = true
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-apply", ID: id}); err == nil {
		t.Fatal("lost response hidden")
	}
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-apply", ID: id}); err != nil {
		t.Fatal(err)
	}
	if f.patches != 1 {
		t.Fatal("duplicate patch")
	}
	newID := quickPreview(t, r)
	f.baseConfig = json.RawMessage(`{"user":"manual"}`)
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-quick-apply", ID: newID}); err == nil || f.patches != 1 {
		t.Fatal("manual edit overwritten")
	}
}
