package notion

import "testing"

func TestParseCurlAcceptsAgentCapture(t *testing.T) {
	source := `curl 'https://app.notion.com/api/v3/createAgentThread' -H 'cookie: notion_user_id=user-agent; token_v2=secret' -H 'x-notion-active-user-header: user-agent' -H 'x-notion-space-id: space-agent' -H 'content-type: application/json' --data-raw '{"type":"personal_agent","spaceId":"space-agent","threadId":"thread-agent","content":[{"type":"text","text":[["hello"]]}]}'`
	cfg, err := ParseCurl(source)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CaptureMode != ModeV3 || cfg.UserID != "user-agent" || cfg.SpaceID != "space-agent" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.Template) != 0 {
		t.Fatalf("agent body must not become a legacy template: %#v", cfg.Template)
	}
}

func TestParseCurlAcceptsBashANSICBody(t *testing.T) {
	source := `curl 'https://app.notion.com/api/v3/createAgentThread' -H 'cookie: notion_user_id=user-agent; token_v2=secret' -H 'x-notion-active-user-header: user-agent' -H 'x-notion-space-id: space-agent' --data-raw $'{"type":"personal_agent","spaceId":"space-agent","content":[{"type":"text","text":[["hello\\u0021"]]}]}'`
	cfg, err := ParseCurl(source)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CaptureMode != ModeV3 || cfg.SpaceID != "space-agent" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseCurlDetectsWorkflowV2(t *testing.T) {
	source := `curl 'https://app.notion.com/api/v3/runInferenceTranscript' -H 'cookie: notion_user_id=user-v2; token_v2=secret' -H 'x-notion-active-user-header: user-v2' -H 'content-type: application/json' --data-raw '{"spaceId":"space-v2","expectedFundingRoute":"ordinary","submittedUserStepId":"step-v2","transcript":[{"id":"cfg","type":"config","value":{"enableScriptAgent":true,"enableAgentSkillsV2":true}},{"id":"ctx","type":"context","value":{"userId":"user-v2","spaceViewId":"view-v2"}},{"id":"step-v2","type":"user","value":[["hello"]]}]}'`
	cfg, err := ParseCurl(source)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CaptureMode != ModeV2 || cfg.SpaceViewID != "view-v2" || len(cfg.Template) == 0 {
		t.Fatalf("unexpected V2 config: %#v", cfg)
	}
}

func TestNormalizeChatModeMigratesAliases(t *testing.T) {
	cases := map[string]string{"agent": ModeV3, "agent-service": ModeV3, "workflow_v2": ModeV2, "legacy": ModeLegacy, "": ModeV2}
	for input, want := range cases {
		if got := NormalizeChatMode(input); got != want {
			t.Fatalf("NormalizeChatMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseCurlKeepsLegacyTemplate(t *testing.T) {
	source := `curl 'https://app.notion.com/api/v3/runInferenceTranscript' -H 'cookie: notion_user_id=user-legacy; token_v2=secret' -H 'x-notion-active-user-header: user-legacy' -H 'content-type: application/json' --data-raw '{"spaceId":"space-legacy","transcript":[{"type":"context","value":{"userId":"user-legacy","spaceViewId":"view-legacy"}}]}'`
	cfg, err := ParseCurl(source)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CaptureMode != ModeLegacy || cfg.SpaceViewID != "view-legacy" || len(cfg.Template) == 0 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestSessionHasUserUsesMultiAccountCookie(t *testing.T) {
	headers := map[string]string{"cookie": "notion_user_id=user-a; notion_users=%5B%22user-a%22%2C%22user-b%22%5D"}
	if known, present := sessionHasUser(headers, "user-b"); !known || !present {
		t.Fatalf("expected user-b in multi-account session: known=%v present=%v", known, present)
	}
	if known, present := sessionHasUser(headers, "user-c"); !known || present {
		t.Fatalf("expected user-c to be absent: known=%v present=%v", known, present)
	}
}

func TestModelsAllowedByPolicy(t *testing.T) {
	policy := workspaceModelPolicy{
		found:             true,
		disabledModels:    map[string]bool{"blocked-model": true},
		disabledProviders: map[string]bool{"blocked-provider": true},
	}
	models := modelsAllowedByPolicy([]Model{
		{ID: "allowed", Provider: "ok"},
		{ID: "blocked-model", Provider: "ok"},
		{ID: "provider-model", Provider: "blocked-provider"},
	}, policy)
	if len(models) != 1 || models[0].ID != "allowed" {
		t.Fatalf("unexpected models: %#v", models)
	}
}
