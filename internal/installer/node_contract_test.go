package installer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestUnifiedCoreUsesModernPanelVersionAndOfficialCatalog(t *testing.T) {
	for _, current := range []string{"26.9.9", "26.11.9", "26.12.1"} {
		t.Run(current, func(t *testing.T) {
			r, _, j := updateFixture(t)
			j.Version = 4
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
			}
			if err := r.saveNodeJob(&j); err != nil {
				t.Fatal(err)
			}
			r.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				body := `{"response":{"uuid":"` + j.NodeUUID + `","address":"` + j.Params.Address + `","isConnected":true,"versions":{"xray":"` + current + `","node":"2.7.0"},"xrayUptime":10,"system":{"stats":{"uptime":20}}}}`
				if req.URL.Host == "api.github.com" {
					switch req.URL.RequestURI() {
					case "/repos/XTLS/Xray-core/releases/latest":
						body = `{"tag_name":"v26.11.9"}`
					case "/repos/XTLS/Xray-core/releases?per_page=100":
						body = `[{"tag_name":"v26.11.9"},{"tag_name":"v26.10.31"},{"tag_name":"v26.9.9"}]`
					default:
						t.Fatal("unexpected release request", req.URL)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			result, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-details", UUID: j.NodeUUID})
			if err != nil || len(result.Nodes) != 1 || result.Nodes[0].CoreVersion() != current {
				t.Fatal(result, err)
			}
			result, err = r.manageNodes(context.Background(), services.NodeRequest{Action: "node-core-check", UUID: j.NodeUUID})
			if err != nil {
				t.Fatal(err)
			}
			if current == "26.9.9" {
				if result.Version != "v26.11.9" || len(result.Versions) != 0 {
					t.Fatal("missing update", result)
				}
			} else if result.NoChange || len(result.Versions) < 2 || !strings.Contains(result.TextRU, "актуальная") {
				t.Fatal("missing rollback choices", result)
			}
		})
	}
}

func TestDeleteNodeUsesExactUUIDAndAcceptsNoContent(t *testing.T) {
	r, _ := managementFixture(t)
	deleted := 0
	nodeTestAPI(t, r, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/nodes/"+testNodeUUID {
			t.Fatal(req.URL)
		}
		if req.Method == "DELETE" {
			deleted++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if req.Method != "GET" {
			t.Fatal(req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": services.Node{UUID: testNodeUUID, Address: "192.0.2.10"}})
	})
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-delete", UUID: testNodeUUID, Value: "wrong"}); err == nil || deleted != 0 {
		t.Fatal("unconfirmed deletion")
	}
	if _, err := r.manageNodes(context.Background(), services.NodeRequest{Action: "node-delete", UUID: testNodeUUID, Value: testNodeUUID}); err != nil || deleted != 1 {
		t.Fatal(err, deleted)
	}
}
