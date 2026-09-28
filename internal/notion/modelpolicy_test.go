package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func modelPolicySpaces(personal, custom map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"user": map[string]interface{}{
			"space": map[string]interface{}{
				"space": map[string]interface{}{
					"value": map[string]interface{}{
						"value": map[string]interface{}{
							"id": "space",
							"settings": map[string]interface{}{
								"personal_agent_model_policy": personal,
								"custom_agent_model_policy":   custom,
							},
						},
					},
				},
			},
		},
	}
}

func TestWorkspaceModelsPolicyUsesAdminCatalogAndProtocolPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case pathGetSpaces:
			_ = json.NewEncoder(w).Encode(modelPolicySpaces(
				map[string]interface{}{"disabledModels": []interface{}{"model-b"}, "disabledProviders": []interface{}{}},
				map[string]interface{}{"disabledModels": []interface{}{}, "disabledProviders": []interface{}{"provider-a"}},
			))
		case "/api/v3/getAvailableModels":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["surface"] != "workspace_model_settings" {
				t.Errorf("surface=%#v", body["surface"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{
				map[string]interface{}{"model": "model-a", "modelMessage": "Model A", "modelProvider": "provider-a"},
				map[string]interface{}{"model": "model-b", "modelMessage": "Model B", "modelProvider": "provider-b"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient()
	defer client.Close()
	client.SetConfig(&Config{Origin: server.URL, Headers: map[string]string{}, SpaceID: "space", UserID: "user"})
	runtime := NewRuntime(client)

	v3, err := runtime.WorkspaceModelsPolicy(context.Background(), ModeV3)
	if err != nil {
		t.Fatal(err)
	}
	if v3.PolicyKey != "personal_agent_model_policy" {
		t.Fatalf("wrong V3 key: %s", v3.PolicyKey)
	}
	states := map[string]bool{}
	for _, model := range v3.Models {
		states[model.ID] = model.Active
	}
	if !states["model-a"] || states["model-b"] {
		t.Fatalf("V3 policy not applied: %#v", states)
	}

	v2, err := runtime.WorkspaceModelsPolicy(context.Background(), ModeV2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.PolicyKey != "custom_agent_model_policy" {
		t.Fatalf("wrong V2 key: %s", v2.PolicyKey)
	}
	for _, model := range v2.Models {
		if model.ID == "model-a" && (model.Active || !model.DisabledByProvider) {
			t.Fatalf("provider policy not reflected: %#v", model)
		}
	}
}

func TestUpdateWorkspaceModelsPolicyRewritesFullArrays(t *testing.T) {
	for _, tc := range []struct{ mode, key string }{
		{ModeV2, "custom_agent_model_policy"},
		{ModeV3, "personal_agent_model_policy"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var update map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				switch r.URL.Path {
				case pathGetSpaces:
					_ = json.NewEncoder(w).Encode(modelPolicySpaces(
						map[string]interface{}{"disabledModels": []interface{}{}, "disabledProviders": []interface{}{}},
						map[string]interface{}{"disabledModels": []interface{}{}, "disabledProviders": []interface{}{}},
					))
				case "/api/v3/getAvailableModels":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{
						map[string]interface{}{"model": "a1", "modelMessage": "A1", "modelProvider": "a"},
						map[string]interface{}{"model": "a2", "modelMessage": "A2", "modelProvider": "a"},
						map[string]interface{}{"model": "b1", "modelMessage": "B1", "modelProvider": "b"},
					}})
				case pathUpdateSpaceSettings:
					if r.Header.Get("accept") != "*/*" || r.Header.Get("x-notion-space-id") != "space" {
						t.Errorf("missing browser headers: %#v", r.Header)
					}
					_ = json.NewDecoder(r.Body).Decode(&update)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
				default:
					http.NotFound(w, r)
				}
			}))

			client := NewClient()
			client.SetConfig(&Config{Origin: server.URL, Headers: map[string]string{}, SpaceID: "space", UserID: "user"})
			runtime := NewRuntime(client)
			result, err := runtime.UpdateWorkspaceModelsPolicy(context.Background(), tc.mode, []string{"a1"})
			client.Close()
			server.Close()
			if err != nil {
				t.Fatal(err)
			}
			patch := update["settingsPatch"].(map[string]interface{})
			if len(patch) != 1 || patch[tc.key] == nil {
				t.Fatalf("wrong settings patch: %#v", patch)
			}
			policy := patch[tc.key].(map[string]interface{})
			if !reflect.DeepEqual(policy["disabledModels"], []interface{}{"a2"}) {
				t.Fatalf("disabledModels=%#v", policy["disabledModels"])
			}
			if !reflect.DeepEqual(policy["disabledProviders"], []interface{}{"b"}) {
				t.Fatalf("disabledProviders=%#v", policy["disabledProviders"])
			}
			if !reflect.DeepEqual(update["unsetSettingKeys"], []interface{}{}) {
				t.Fatalf("unsetSettingKeys=%#v", update["unsetSettingKeys"])
			}
			active := map[string]bool{}
			for _, model := range result.Models {
				if model.ID == "a1" || model.ID == "a2" || model.ID == "b1" {
					active[model.ID] = model.Active
				}
			}
			if !active["a1"] || active["a2"] || active["b1"] {
				t.Fatalf("unexpected result state: %#v", active)
			}
		})
	}
}
