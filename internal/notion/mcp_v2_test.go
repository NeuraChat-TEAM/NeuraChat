package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestConnectMcpUsesExactWorkflowV2Chain(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bodies []map[string]interface{}
	moduleID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("accept") != "*/*" {
			t.Errorf("%s accept=%q", r.URL.Path, r.Header.Get("accept"))
		}
		if r.Header.Get("referer") == "" {
			t.Errorf("%s has no V2 /ai referer", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		response := map[string]interface{}{}
		switch r.URL.Path {
		case pathValidate:
			response["tools"] = []interface{}{map[string]interface{}{"name": "read"}}
		case pathConnect:
			moduleID, _ = body["integrationId"].(string)
		case pathSyncRecords:
			response["recordMap"] = map[string]interface{}{"space_view": map[string]interface{}{
				"view": map[string]interface{}{"value": map[string]interface{}{"value": map[string]interface{}{
					"settings": map[string]interface{}{"library": map[string]interface{}{"version": 11}},
				}}},
			}}
		case pathIntegrationModal:
			response["surface"] = map[string]interface{}{
				"status": map[string]interface{}{"state": "connected", "reasons": []interface{}{}},
				"connectedAgents": []interface{}{map[string]interface{}{
					"moduleId": moduleID, "externalConnectionId": "connection",
				}},
			}
		case pathExternalConnections:
			response["connections"] = []interface{}{map[string]interface{}{
				"id": "connection", "status": "connected",
				"data": map[string]interface{}{"serverUrl": "https://example.test/mcp"},
			}}
		case pathExternalConnection:
			response["connection"] = map[string]interface{}{"id": "connection", "status": "connected"}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient()
	defer client.Close()
	client.SetConfig(&Config{
		Origin: server.URL, Headers: map[string]string{}, SpaceID: "space", UserID: "user",
		SpaceViewID: "view", CaptureMode: ModeV2, Template: map[string]interface{}{"transcript": []interface{}{}},
	})
	module, err := client.ConnectMcp(context.Background(), ConnectMcpInput{
		Name: "Renamed MCP", ServerURL: "https://example.test/mcp", AutoRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if module.ConnectionID != "connection" || module.ConnectionState != "connected" || module.Enabled {
		t.Fatalf("unexpected module: %#v", module)
	}
	if !module.ReadAutoRun || !module.WriteAutoRun {
		t.Fatalf("auto-run settings lost: %#v", module)
	}

	wantPaths := []string{
		pathOAuthSupport, pathValidate, pathConnect, pathSyncRecords, PathTransactions,
		pathIntegrationModal, pathExternalConnections, pathExternalConnection,
		pathModuleSetting, pathModuleSetting,
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths\n got: %#v\nwant: %#v", paths, wantPaths)
	}

	save := bodies[4]
	transactions := save["transactions"].([]interface{})
	operations := transactions[0].(map[string]interface{})["operations"].([]interface{})
	args := operations[0].(map[string]interface{})["args"].(map[string]interface{})
	entries := args["agent_chat_modules"].([]interface{})
	if entries[0].(map[string]interface{})["defaultEnabled"] != false {
		t.Fatalf("V2 attachment must use defaultEnabled=false: %#v", entries[0])
	}
	writeSettings := bodies[8]["settings"].(map[string]interface{})
	readSettings := bodies[9]["settings"].(map[string]interface{})
	if !reflect.DeepEqual(writeSettings, map[string]interface{}{"runWriteToolsAutomatically": true}) ||
		!reflect.DeepEqual(readSettings, map[string]interface{}{"runReadToolsAutomatically": true}) {
		t.Fatalf("settings were not sent independently: %#v / %#v", writeSettings, readSettings)
	}
}

func TestConnectMcpRejectsNonV2Capture(t *testing.T) {
	client := NewClient()
	defer client.Close()
	client.SetConfig(&Config{Origin: "https://example.test", Headers: map[string]string{}, SpaceID: "space", CaptureMode: ModeV3})
	if _, err := client.ConnectMcp(context.Background(), ConnectMcpInput{ServerURL: "https://example.test/mcp"}); err == nil {
		t.Fatal("V3 capture unexpectedly used the Workflow V2 MCP chain")
	}
}

func TestMcpServerIdentitySurvivesRenameAndRotatingAuthQuery(t *testing.T) {
	a := mcpServerIdentity("HTTPS://Example.Test/mcp/?token=old")
	b := mcpServerIdentity("https://example.test/mcp?token=new")
	if a != b {
		t.Fatalf("same renamed MCP endpoint did not match: %q != %q", a, b)
	}
}
