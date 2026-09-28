package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func v2CaptureTemplate() map[string]interface{} {
	return map[string]interface{}{
		"expectedFundingRoute": "ordinary",
		"threadParentPointer": map[string]interface{}{
			"table": "space", "id": "space", "spaceId": "space",
		},
		"transcript": []interface{}{
			map[string]interface{}{
				"id": "captured-config", "type": "config",
				"value": map[string]interface{}{
					"type": "workflow", "enableScriptAgent": true,
					"availableConnectors": []interface{}{},
					"searchScopes":        []interface{}{map[string]interface{}{"type": "everything"}},
					"useWebSearch":        true, "internetAccess": true,
					"agentMemorySettings": map[string]interface{}{
						"useMemories": true, "excludeChatFromMemories": false,
					},
					"enableSuggestedEditsTools": true,
				},
			},
			map[string]interface{}{
				"id": "captured-context", "type": "context",
				"value": map[string]interface{}{
					"userId": "user", "spaceId": "space", "spaceViewId": "view",
					"surface": "ai_module",
				},
			},
			map[string]interface{}{
				"id": "captured-user", "type": "user", "userId": "user",
				"value": []interface{}{[]interface{}{"captured"}},
			},
		},
	}
}

func stepTypes(raw interface{}) []string {
	items, _ := raw.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		step, _ := item.(map[string]interface{})
		kind, _ := step["type"].(string)
		out = append(out, kind)
	}
	return out
}

func TestFreshV2NormalizesContinuationCapture(t *testing.T) {
	template := v2CaptureTemplate()
	steps := template["transcript"].([]interface{})
	config := steps[0].(map[string]interface{})["value"].(map[string]interface{})
	delete(config, "availableConnectors")
	config["isThreadStartedByAdmin"] = true
	config["canAccessAllAgentThreads"] = false
	config["useContextualCoreDocsAutoLoad"] = false
	config["useDocPreviewsForCoreAutoLoad"] = false
	config["model"] = "captured-continuation-model"
	config["reasoningEffort"] = "high"
	config["modelFromUser"] = false
	config["updatePageStaleViewGuardEnabled"] = true

	runtime := NewRuntime(nil)
	body, _, _, err := runtime.buildBody(&Config{
		SpaceID: "space", UserID: "user", CaptureMode: ModeV2, Template: template,
	}, ChatRequest{
		ConversationID: "fresh", Mode: ModeV2, SearchAllSources: true,
		BrowserEnabled: true, UseMemories: true, SuggestedEdits: true,
		Messages: []Message{{ID: "user-step", Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := body["transcript"].([]interface{})[0].(map[string]interface{})["value"].(map[string]interface{})
	for _, key := range []string{
		"isThreadStartedByAdmin", "canAccessAllAgentThreads",
		"useContextualCoreDocsAutoLoad", "useDocPreviewsForCoreAutoLoad",
		"model", "reasoningEffort",
	} {
		if _, ok := out[key]; ok {
			t.Fatalf("fresh V2 config retained continuation-only %s", key)
		}
	}
	if _, ok := out["availableConnectors"].([]interface{}); !ok {
		t.Fatalf("fresh V2 config has no availableConnectors: %#v", out)
	}
	if out["updatePageStaleViewGuardEnabled"] != false || out["modelFromUser"] != false {
		t.Fatalf("fresh V2 flags were not normalized: %#v", out)
	}
}

func TestPersistWorkflowV2StepsIncludesUploadedAttachment(t *testing.T) {
	requests := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != transactionsFanoutPath {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests <- body
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient()
	client.http = server.Client()
	cfg := &Config{
		Origin: server.URL, SpaceID: "space", UserID: "user", CaptureMode: ModeV2,
		Headers: map[string]string{}, Template: v2CaptureTemplate(),
	}
	client.SetConfig(cfg)
	runtime := NewRuntime(client)
	req := ChatRequest{
		ConversationID: "local-upload", Mode: ModeV2,
		Messages: []Message{{ID: "first", Role: "user", Content: "first"}},
	}
	_, convo, _, err := runtime.buildBody(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	convo.Started = true
	req.Messages = append(req.Messages, Message{
		ID: "second", Role: "user", Content: "Реши",
		Attachments: []Attachment{{
			StepID: "attachment-step", StepType: "attachment",
			FileURL: "attachment:file-id:image.png", FileName: "image.png",
			ContentType: "image/png", Metadata: map[string]interface{}{"fileSizeBytes": 91212},
		}},
	})
	body, convo, _, err := runtime.buildBody(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.persistWorkflowV2Steps(context.Background(), cfg, convo, body); err != nil {
		t.Fatal(err)
	}

	saved := <-requests
	transactions := saved["transactions"].([]interface{})
	operations := transactions[0].(map[string]interface{})["operations"].([]interface{})
	var types []string
	for _, raw := range operations {
		op := raw.(map[string]interface{})
		if op["command"] != "set" {
			continue
		}
		args := op["args"].(map[string]interface{})
		step := args["step"].(map[string]interface{})
		types = append(types, step["type"].(string))
	}
	want := []string{"context", "attachment", "updated-config", "user"}
	if len(types) != len(want) {
		t.Fatalf("unexpected persisted steps: %#v", types)
	}
	for index := range want {
		if types[index] != want[index] {
			t.Fatalf("unexpected persisted steps: %#v", types)
		}
	}
}

func TestPatchStartSnapshotSurfacesNestedV2Error(t *testing.T) {
	runtime := NewRuntime(nil)
	events := runtime.handleFrame(&conversation{}, NewAccumulator(), map[string]interface{}{
		"type": "patch-start",
		"data": map[string]interface{}{"s": []interface{}{map[string]interface{}{
			"type": "error", "message": "Something went wrong. Please try again later.",
			"subType": "temporarily-unavailable",
		}}},
	})
	if len(events) != 1 || events[0].Type != "error" || events[0].Message == "" {
		t.Fatalf("nested stream error was lost: %#v", events)
	}
}

func TestNotionTimestampMatchesBrowserV2Shape(t *testing.T) {
	got := notionTimestamp(time.Date(2026, 9, 26, 22, 37, 52, 460000000, time.FixedZone("MSK", 3*60*60)))
	if got != "2026-09-26T22:37:52.460+03:00" {
		t.Fatalf("unexpected timestamp: %s", got)
	}
}

func TestBuildBodyV2MatchesFreshChatContractAndDoesNotInventConfigKeys(t *testing.T) {
	runtime := NewRuntime(nil)
	cfg := &Config{SpaceID: "space", UserID: "user", CaptureMode: ModeV2, Template: v2CaptureTemplate()}
	req := ChatRequest{
		ConversationID: "local", Mode: ModeV2, SystemPrompt: "system instructions",
		SearchAllSources: true, BrowserEnabled: true, UseMemories: true, SuggestedEdits: true,
		Messages: []Message{{ID: "local-user", Role: "user", Content: "hello"}},
	}

	body, convo, _, err := runtime.buildBody(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if body["createThread"] != true || body["generateTitle"] != true || body["isPartialTranscript"] != false {
		t.Fatalf("unexpected first-turn flags: %#v", body)
	}
	if body["submittedUserStepId"] == "" || body["spaceId"] != "space" || body["expectedFundingRoute"] != "ordinary" {
		t.Fatalf("missing V2 routing fields: %#v", body)
	}
	types := stepTypes(body["transcript"])
	want := []string{"config", "context", "user"}
	if len(types) != len(want) {
		t.Fatalf("unexpected transcript types: %#v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("unexpected transcript types: %#v", types)
		}
	}

	steps := body["transcript"].([]interface{})
	config := steps[0].(map[string]interface{})["value"].(map[string]interface{})
	if _, ok := config["customInstructions"]; ok {
		t.Fatal("V2 config must not invent customInstructions")
	}
	if _, ok := config["systemPrompt"]; ok {
		t.Fatal("V2 config must not invent systemPrompt")
	}
	user := steps[2].(map[string]interface{})
	if user["userId"] != "user" {
		t.Fatalf("missing userId: %#v", user)
	}
	value := user["value"].([]interface{})[0].([]interface{})[0]
	if value != "system instructions\n\nhello" {
		t.Fatalf("prompt was not safely prepended to the user step: %#v", value)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2})$`).MatchString(user["createdAt"].(string)) {
		t.Fatalf("unexpected createdAt: %v", user["createdAt"])
	}

	// The continuation in the HAR contains config, initial context, current
	// context, updated-config, and user in exactly this order.
	convo.Started = true
	req.Messages = append(req.Messages, Message{ID: "second", Role: "user", Content: "again"})
	body, _, _, err = runtime.buildBody(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	types = stepTypes(body["transcript"])
	want = []string{"config", "context", "context", "updated-config", "user"}
	if len(types) != len(want) {
		t.Fatalf("unexpected continuation transcript types: %#v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("unexpected continuation transcript types: %#v", types)
		}
	}
	if body["createThread"] != false || body["generateTitle"] != false || body["isPartialTranscript"] != true {
		t.Fatalf("unexpected continuation flags: %#v", body)
	}
}
