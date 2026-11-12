package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

const routeBaseID = "11111111-2222-4333-8444-555555555555"
const routeNewID = "21111111-2222-4333-8444-555555555555"
const routeHostID = "31111111-2222-4333-8444-555555555555"
const routeNodeID = "41111111-2222-4333-8444-555555555555"
const routeTestConfig = `{"inbounds":[{"tag":"socks","listen":"127.0.0.1","port":10808,"protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`

type routingAPIFake struct {
	exists, loseCreate, losePatch, loseDelete, assigned bool
	creates, patches, deletes                           int
	name                                                string
	config                                              json.RawMessage
	baseConfig                                          json.RawMessage
	writes                                              []string
}

func routingFixture(t *testing.T) (*Real, *routingAPIFake) {
	t.Helper()
	r, _ := managementFixture(t)
	f := &routingAPIFake{}
	r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(req.URL.Path, "/api")
		if req.Method != "GET" {
			f.writes = append(f.writes, req.Method+" "+path)
		}
		var response any
		switch {
		case req.Method == "GET" && path == "/nodes":
			response = map[string]any{"nodes": []services.Node{{UUID: routeNodeID, Name: "FN1501", Address: "192.0.2.10"}}}
		case req.Method == "GET" && path == "/hosts":
			values := []services.RoutingHost{{UUID: routeHostID, Remark: "FN1501", Address: "192.0.2.10", Nodes: []string{routeNodeID}}}
			if f.assigned {
				values[0].TemplateUUID = routeNewID
			}
			response = map[string]any{"hosts": values}
		case req.Method == "GET" && path == "/subscription-templates":
			values := []services.RoutingTemplate{{UUID: routeBaseID, Name: "Default", Type: "XRAY_JSON"}}
			if f.exists {
				values = append(values, services.RoutingTemplate{UUID: routeNewID, Name: f.name, Type: "XRAY_JSON"})
			}
			response = map[string]any{"templates": values, "total": len(values)}
		case req.Method == "GET" && path == "/subscription-templates/"+routeBaseID:
			config := f.baseConfig
			if config == nil {
				config = json.RawMessage(routeTestConfig)
			}
			response = services.RoutingTemplate{UUID: routeBaseID, Name: "Default", Type: "XRAY_JSON", Config: config}
		case req.Method == "POST" && path == "/subscription-templates":
			var body map[string]string
			_ = json.NewDecoder(req.Body).Decode(&body)
			f.name = body["name"]
			f.exists = true
			f.creates++
			f.config = json.RawMessage(routeTestConfig)
			if f.loseCreate {
				f.loseCreate = false
				return nil, errors.New("lost create acknowledgement")
			}
			response = services.RoutingTemplate{UUID: routeNewID, Name: f.name, Type: "XRAY_JSON", Config: f.config}
		case req.Method == "PATCH" && path == "/subscription-templates":
			var body struct {
				UUID   string          `json:"uuid"`
				Config json.RawMessage `json:"templateJson"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.UUID != routeNewID && body.UUID != routeBaseID {
				t.Fatal("base/default template was modified")
			}
			if body.UUID == routeBaseID {
				f.baseConfig = body.Config
			} else {
				f.config = body.Config
			}
			f.patches++
			if f.losePatch {
				f.losePatch = false
				return nil, errors.New("lost patch acknowledgement")
			}
			response = map[string]any{}
		case req.Method == "GET" && path == "/subscription-templates/"+routeNewID && f.exists:
			response = services.RoutingTemplate{UUID: routeNewID, Name: f.name, Type: "XRAY_JSON", Config: f.config}
		case req.Method == "DELETE" && path == "/subscription-templates/"+routeNewID:
			f.exists = false
			f.deletes++
			if f.loseDelete {
				f.loseDelete = false
				return nil, errors.New("lost delete acknowledgement")
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		case req.Method == "GET" && (path == "/external-squads" || path == "/subscription-settings"):
			response = map[string]any{}
		default:
			t.Fatal("unexpected panel operation", req.Method, path)
		}
		data, _ := json.Marshal(map[string]any{"response": response})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})
	return r, f
}

func routePreview(t *testing.T, r *Real) string {
	t.Helper()
	spec := services.RoutingSpec{Name: "Client_route", BaseTemplateUUID: routeBaseID, DefaultTarget: "main", Pools: []services.RoutingPool{{Name: "main", Hosts: []string{routeHostID}}}, Rules: []services.RoutingRule{{Target: "direct", Domains: []string{"domain:ru"}}}}
	result, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-preview", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return result.Scheme.ID
}

func TestRoutingTemplateCreateResumeAndNoHostProfileOrSquadChanges(t *testing.T) {
	for _, failure := range []string{"none", "create-response", "patch-response"} {
		t.Run(failure, func(t *testing.T) {
			r, f := routingFixture(t)
			id := routePreview(t, r)
			if len(f.writes) != 0 {
				t.Fatal("preview mutated panel")
			}
			f.loseCreate = failure == "create-response"
			f.losePatch = failure == "patch-response"
			result, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-apply", ID: id})
			if failure != "none" {
				if err == nil {
					t.Fatal("lost response hidden")
				}
				result, err = r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-apply", ID: id})
			}
			if err != nil || result.Scheme.Phase != "ready" || f.creates != 1 || f.patches != 1 {
				t.Fatal(result, err, f.creates, f.patches)
			}
			for _, write := range f.writes {
				if write != "POST /subscription-templates" && write != "PATCH /subscription-templates" {
					t.Fatal("connection/profile/squad mutated", write)
				}
			}
		})
	}
}

func TestRoutingDeletionProtectsAssignedTemplatesAndResumesLostResponse(t *testing.T) {
	r, f := routingFixture(t)
	id := routePreview(t, r)
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-apply", ID: id}); err != nil {
		t.Fatal(err)
	}
	f.assigned = true
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-delete", ID: id}); err == nil || f.deletes != 0 {
		t.Fatal("assigned template deleted")
	}
	f.assigned = false
	f.loseDelete = true
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-delete", ID: id}); err == nil {
		t.Fatal("lost response hidden")
	}
	result, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-delete", ID: id})
	if err != nil || result.Scheme.Phase != "removed" || f.deletes != 1 {
		t.Fatal(result, err, f.deletes)
	}
}

func TestReadyRoutingTemplateCannotOverwriteManualEdits(t *testing.T) {
	r, f := routingFixture(t)
	id := routePreview(t, r)
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-apply", ID: id}); err != nil {
		t.Fatal(err)
	}
	f.config = json.RawMessage(`{"user":"manual edit"}`)
	if _, err := r.manageRouting(context.Background(), services.RoutingRequest{Action: "route-apply", ID: id}); err == nil || f.patches != 1 {
		t.Fatal("manual edit overwritten")
	}
}

func TestRoutingStatusDoesNotExposeOrDestroyServiceCredentials(t *testing.T) {
	spec := services.RoutingSpec{Exits: []services.RoutingExit{{Name: "bridge", URL: "vless://private-service-credential"}, {Name: "warp", WireGuard: &services.RoutingWireGuard{SecretKey: "private-warp-key"}}}}
	status := schemeStatus(routingJob{Spec: spec})
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "private-") || spec.Exits[0].URL == "" || spec.Exits[1].WireGuard.SecretKey == "" {
		t.Fatal("status leaked credentials or destroyed the saved spec")
	}
}

func TestJSONHashIgnoresPanelObjectKeyOrder(t *testing.T) {
	if jsonHash(json.RawMessage(`{"b":2,"a":{"y":1,"x":0}}`)) != jsonHash(json.RawMessage(`{"a":{"x":0,"y":1},"b":2}`)) {
		t.Fatal("panel JSON object ordering was treated as a manual edit")
	}
}
