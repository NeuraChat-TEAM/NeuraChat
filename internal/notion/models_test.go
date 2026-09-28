package notion

import "testing"

func TestModelsAllowedByPersonalAgentPolicy(t *testing.T) {
	policy := workspaceModelPolicy{
		found: true,
		disabledModels: stringSet([]interface{}{
			"almond-croissant-low", "angel-cake-high", "apricot-sorbet-high",
			"ambrosia-tart-high", "anthropic-haiku-4.5", "orchid-muffin",
			"olive-jellyroll", "oatmeal-cookie", "oval-kumquat-medium",
			"opal-quince-medium", "omodi",
		}),
		disabledProviders: stringSet([]interface{}{
			"glm", "kimi", "deepseek", "xai", "gemini",
		}),
	}

	got := modelsAllowedByPolicy(knownModels(), policy)
	want := map[string]bool{
		"agave-flan": true, "avocado-froyo-medium": true,
		"assam-chai": true, "acai-budino-high": true,
		"orlando-quinn": true, "orange-mousse": true,
		"oregon-grape-medium": true, "otaheite-apple-medium": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d active models, want %d: %#v", len(got), len(want), got)
	}
	for _, model := range got {
		if !want[model.ID] {
			t.Fatalf("disabled model leaked into picker: %s", model.ID)
		}
	}
}

func TestModelsAllowedByPolicyFiltersProviderAndModel(t *testing.T) {
	catalog := []Model{
		{ID: "allowed", Provider: "anthropic"},
		{ID: "blocked-model", Provider: "anthropic"},
		{ID: "blocked-provider-model", Provider: "openai"},
	}
	policy := workspaceModelPolicy{
		disabledModels:    map[string]bool{"blocked-model": true},
		disabledProviders: map[string]bool{"openai": true},
	}
	got := modelsAllowedByPolicy(catalog, policy)
	if len(got) != 1 || got[0].ID != "allowed" {
		t.Fatalf("unexpected filtered models: %#v", got)
	}
}
