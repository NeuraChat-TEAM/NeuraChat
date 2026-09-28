package notion

import (
	"strings"
	"testing"
)

func TestAgentProjectorKeepsOnlyCurrentTurnAndAppliesToolPatch(t *testing.T) {
	projector := &agentProjector{expectedClientMessageID: "current-message"}
	page := map[string]interface{}{
		"session": map[string]interface{}{"status": "idle"},
		"patches": []interface{}{
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "old-user", "kind": "user_message", "sequence": float64(1),
				"client_message_id": "old-message", "text": []interface{}{[]interface{}{"old question"}},
			}},
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "old-answer", "kind": "assistant_message", "sequence": float64(2),
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "OLD ANSWER"}},
			}},
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "current-user", "kind": "user_message", "sequence": float64(3),
				"client_message_id": "current-message", "text": []interface{}{[]interface{}{"current question"}},
			}},
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "thinking", "kind": "thinking", "sequence": float64(4), "content_text": "CURRENT THINKING",
			}},
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "tool:call-1", "kind": "tool", "sequence": float64(5),
				"tool_use_id": "call-1", "name": "mcp_run_tool",
				"text": map[string]interface{}{
					"running":  []interface{}{[]interface{}{"notcode", " / ", "NotCode status"}},
					"finished": []interface{}{[]interface{}{"notcode", " / ", "NotCode status"}},
				},
			}},
			map[string]interface{}{"op": "patch", "id": "tool:call-1", "ops": []interface{}{
				map[string]interface{}{"op": "add", "path": "/result", "value": map[string]interface{}{"result_text": "ok"}},
			}},
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "current-answer", "kind": "assistant_message", "sequence": float64(6),
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "CURRENT ANSWER"}},
			}},
		},
	}

	status, events := projector.apply(page)
	if status != "idle" {
		t.Fatalf("unexpected status: %q", status)
	}
	var joined strings.Builder
	seenCall, seenResult := false, false
	for _, event := range events {
		joined.WriteString(event.Delta)
		if event.Type == "tool-call" && event.ID == "call-1" {
			seenCall = true
			if event.Server != "notcode" || event.Name != "NotCode status" {
				t.Fatalf("tool display label was not decoded: %#v", event)
			}
		}
		if event.Type == "tool-result" && event.ID == "call-1" {
			seenResult = true
		}
	}
	text := joined.String()
	if strings.Contains(text, "OLD ANSWER") {
		t.Fatalf("historical answer leaked into current turn: %q", text)
	}
	if !strings.Contains(text, "CURRENT THINKING") || !strings.Contains(text, "CURRENT ANSWER") {
		t.Fatalf("current turn was not projected: %q", text)
	}
	if !seenCall || !seenResult {
		t.Fatalf("tool patch was not projected: call=%v result=%v events=%#v", seenCall, seenResult, events)
	}
}

func TestAgentProjectorWaitsForExpectedMessage(t *testing.T) {
	projector := &agentProjector{expectedClientMessageID: "not-yet-visible"}
	_, events := projector.apply(map[string]interface{}{
		"patches": []interface{}{
			map[string]interface{}{"op": "put", "entity": map[string]interface{}{
				"id": "old-answer", "kind": "assistant_message", "sequence": float64(9),
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "OLD ANSWER"}},
			}},
		},
	})
	if len(events) != 0 {
		t.Fatalf("events emitted before current user message appeared: %#v", events)
	}
}

func TestAgentMessageContentIncludesUploadedFile(t *testing.T) {
	content := agentMessageContent("изменил", []Attachment{{AgentFileID: "file-123"}})
	if len(content) != 2 {
		t.Fatalf("expected text and file parts, got %#v", content)
	}
	file, _ := content[1].(map[string]interface{})
	if file["type"] != "file" || file["file_id"] != "file-123" {
		t.Fatalf("unexpected file part: %#v", file)
	}
}

func TestAgentEditAndInterruptPayloadsMatchHAR(t *testing.T) {
	payload := agentTurnPayload(
		"space", "thread", "client-event", "изменил",
		agentMessageContent("изменил", []Attachment{{AgentFileID: "file-123"}}),
		nil, &userStep{Content: "до изменения", Sequence: 362},
	)
	if _, exists := payload["event"]; exists {
		t.Fatalf("edit payload must use events: %#v", payload)
	}
	events, _ := payload["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("expected rewind and message events: %#v", payload)
	}
	rewind, _ := events[0].(map[string]interface{})
	if rewind["type"] != "user.rewind" || rewind["rewind_to_sequence"] != int64(362) {
		t.Fatalf("bad rewind event: %#v", rewind)
	}
	interrupt := agentInterruptPayload("space", "thread")
	event, _ := interrupt["event"].(map[string]interface{})
	if event["type"] != "user.interrupt" {
		t.Fatalf("bad interrupt event: %#v", interrupt)
	}
}

func TestRuntimeKeepsLegacyAndAgentThreadMappingsSeparate(t *testing.T) {
	runtime := NewRuntime(nil)
	runtime.SetThreadStore(
		func(_, _, mode string) string {
			if mode == ModeV3 {
				return "v3-thread"
			}
			if mode == ModeV2 {
				return "v2-thread"
			}
			return "legacy-thread"
		},
		func(_, _, _, _ string) error { return nil },
	)
	runtime.mu.Lock()
	legacy := runtime.state("local", "space", ModeLegacy)
	v2 := runtime.state("local", "space", ModeV2)
	agent := runtime.state("local", "space", ModeV3)
	runtime.mu.Unlock()
	if legacy.ThreadID != "legacy-thread" || legacy.AgentThread {
		t.Fatalf("bad legacy mapping: %#v", legacy)
	}
	if v2.ThreadID != "v2-thread" || v2.AgentThread {
		t.Fatalf("bad V2 mapping: %#v", v2)
	}
	if agent.ThreadID != "v3-thread" || !agent.AgentThread {
		t.Fatalf("bad V3 mapping: %#v", agent)
	}
}
