package notion

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const pathUpdateSpaceSettings = "/api/v3/updateSpaceSettings"

// WorkspaceModel is one row in Settings -> workspace model availability.
// Active is the effective result of both disabledModels and disabledProviders.
type WorkspaceModel struct {
	ID                 string `json:"id"`
	Label              string `json:"label"`
	Provider           string `json:"provider"`
	Group              string `json:"group"`
	Active             bool   `json:"active"`
	DisabledByProvider bool   `json:"disabledByProvider"`
}

// WorkspaceModelPolicy is deliberately protocol-aware: V3 uses Notion's
// personal-agent policy, while V2/legacy use the custom-agent policy.
type WorkspaceModelPolicy struct {
	SpaceID           string           `json:"spaceId"`
	Mode              string           `json:"mode"`
	PolicyKey         string           `json:"policyKey"`
	Models            []WorkspaceModel `json:"models"`
	DisabledProviders []string         `json:"disabledProviders"`
}

func modelPolicyKey(mode string) string {
	if AgentMode(mode) {
		return "personal_agent_model_policy"
	}
	return "custom_agent_model_policy"
}

func modelFromAvailable(raw interface{}) (Model, bool) {
	item := asMap(raw)
	if item == nil {
		return Model{}, false
	}
	id := strings.TrimSpace(str(item, "model"))
	if id == "" {
		id = strings.TrimSpace(str(item, "id"))
	}
	if id == "" {
		return Model{}, false
	}
	label := strings.TrimSpace(str(item, "modelMessage"))
	if label == "" {
		label = strings.TrimSpace(str(item, "label"))
	}
	if label == "" {
		label = id
	}
	provider := strings.ToLower(strings.TrimSpace(str(item, "modelProvider")))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(str(item, "provider")))
	}
	group := strings.TrimSpace(str(item, "displayGroup"))
	if group == "" {
		group = strings.TrimSpace(str(item, "group"))
	}
	return Model{ID: id, Label: label, Provider: provider, Group: group}, true
}

// workspaceModelCatalog asks for the admin-settings surface because the normal
// picker endpoint may intentionally return an empty/restricted list. The known
// catalog is unioned in as a rollout-safe fallback.
func (r *Runtime) workspaceModelCatalog(ctx context.Context, cfg *Config) ([]Model, error) {
	payload, err := r.client.PostJSON(ctx, "/api/v3/getAvailableModels", map[string]interface{}{
		"spaceId": cfg.SpaceID,
		"surface": "workspace_model_settings",
	})
	catalog := []Model{}
	if err == nil {
		if raw, ok := payload["models"].([]interface{}); ok {
			for _, entry := range raw {
				if model, valid := modelFromAvailable(entry); valid {
					catalog = append(catalog, model)
				}
			}
		}
	}
	// A capture or staged backend can omit the admin catalog. Keep settings
	// usable with every code name already verified from browser traffic.
	if len(catalog) == 0 {
		catalog = append(catalog, knownModels()...)
	}
	byID := make(map[string]Model, len(catalog))
	order := make([]string, 0, len(catalog))
	for _, model := range catalog {
		key := strings.ToLower(strings.TrimSpace(model.ID))
		if key == "" {
			continue
		}
		if existing, seen := byID[key]; seen {
			// Prefer richer live metadata over fallback labels.
			if existing.Provider == "" && model.Provider != "" {
				existing.Provider = model.Provider
			}
			if (existing.Label == "" || existing.Label == existing.ID) && model.Label != "" {
				existing.Label = model.Label
			}
			if existing.Group == "" && model.Group != "" {
				existing.Group = model.Group
			}
			byID[key] = existing
			continue
		}
		byID[key] = model
		order = append(order, key)
	}
	out := make([]Model, 0, len(order))
	for _, key := range order {
		out = append(out, byID[key])
	}
	if len(out) == 0 && err != nil {
		return nil, err
	}
	return out, nil
}

func policyStringList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value, enabled := range set {
		if enabled && strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func workspacePolicyResult(cfg *Config, mode string, catalog []Model, policy workspaceModelPolicy) WorkspaceModelPolicy {
	result := WorkspaceModelPolicy{
		SpaceID:           cfg.SpaceID,
		Mode:              protocolMode(mode),
		PolicyKey:         modelPolicyKey(mode),
		Models:            make([]WorkspaceModel, 0, len(catalog)+len(policy.disabledModels)),
		DisabledProviders: policyStringList(policy.disabledProviders),
	}
	seen := map[string]bool{}
	for _, model := range catalog {
		id := strings.ToLower(strings.TrimSpace(model.ID))
		provider := strings.ToLower(strings.TrimSpace(model.Provider))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		byProvider := provider != "" && policy.disabledProviders[provider]
		result.Models = append(result.Models, WorkspaceModel{
			ID: model.ID, Label: model.Label, Provider: provider, Group: model.Group,
			Active:             !policy.disabledModels[id] && !byProvider,
			DisabledByProvider: byProvider,
		})
	}
	// Never silently drop a disabled model merely because a staged catalog no
	// longer advertises it: show its code so the admin can turn it back on.
	for id := range policy.disabledModels {
		if seen[id] {
			continue
		}
		result.Models = append(result.Models, WorkspaceModel{ID: id, Label: id, Provider: "unknown", Active: false})
	}
	return result
}

// WorkspaceModelsPolicy returns the complete admin catalog with a plain
// active/inactive flag for every model.
func (r *Runtime) WorkspaceModelsPolicy(ctx context.Context, mode string) (WorkspaceModelPolicy, error) {
	cfg, err := r.client.require()
	if err != nil {
		return WorkspaceModelPolicy{}, err
	}
	policy, err := r.workspaceModelsPolicy(ctx, cfg, mode)
	if err != nil {
		return WorkspaceModelPolicy{}, err
	}
	// An omitted key means Notion's default policy: every catalog model is
	// active. The first edit materializes the complete arrays.
	if !policy.found {
		policy.found = true
	}
	catalog, err := r.workspaceModelCatalog(ctx, cfg)
	if err != nil {
		return WorkspaceModelPolicy{}, fmt.Errorf("не удалось получить полный каталог моделей: %w", err)
	}
	return workspacePolicyResult(cfg, mode, catalog, policy), nil
}

// UpdateWorkspaceModelsPolicy rewrites the full policy arrays, matching the
// browser's updateSpaceSettings checkbox behavior from the admin-settings HAR.
func (r *Runtime) UpdateWorkspaceModelsPolicy(ctx context.Context, mode string, activeModelIDs []string) (WorkspaceModelPolicy, error) {
	cfg, err := r.client.require()
	if err != nil {
		return WorkspaceModelPolicy{}, err
	}
	current, err := r.WorkspaceModelsPolicy(ctx, mode)
	if err != nil {
		return WorkspaceModelPolicy{}, err
	}
	active := map[string]bool{}
	for _, id := range activeModelIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			active[id] = true
		}
	}
	known := map[string]bool{}
	providerModels := map[string][]string{}
	for _, model := range current.Models {
		id := strings.ToLower(strings.TrimSpace(model.ID))
		provider := strings.ToLower(strings.TrimSpace(model.Provider))
		if id == "" {
			continue
		}
		known[id] = true
		providerModels[provider] = append(providerModels[provider], id)
	}
	for id := range active {
		if !known[id] {
			return WorkspaceModelPolicy{}, fmt.Errorf("неизвестная модель %q", id)
		}
	}

	disabledModels := map[string]bool{}
	disabledProviders := map[string]bool{}
	for provider, ids := range providerModels {
		allDisabled := provider != "" && provider != "unknown"
		for _, id := range ids {
			if active[id] {
				allDisabled = false
				break
			}
		}
		if allDisabled {
			disabledProviders[provider] = true
			continue
		}
		for _, id := range ids {
			if !active[id] {
				disabledModels[id] = true
			}
		}
	}
	// Preserve provider blocks that the current catalog cannot represent.
	for _, provider := range current.DisabledProviders {
		if _, represented := providerModels[provider]; !represented {
			disabledProviders[provider] = true
		}
	}

	policyKey := modelPolicyKey(mode)
	updateCtx := withHeaderOverrides(ctx, map[string]string{
		"accept":                      "*/*",
		"referer":                     strings.TrimRight(cfg.Origin, "/") + "/ai",
		"x-notion-space-id":           cfg.SpaceID,
		"x-notion-active-user-header": cfg.UserID,
	}, true)
	_, err = r.client.PostJSON(updateCtx, pathUpdateSpaceSettings, map[string]interface{}{
		"spaceId": cfg.SpaceID,
		"settingsPatch": map[string]interface{}{
			policyKey: map[string]interface{}{
				"disabledModels":    policyStringList(disabledModels),
				"disabledProviders": policyStringList(disabledProviders),
			},
		},
		"unsetSettingKeys": []interface{}{},
	})
	if err != nil {
		return WorkspaceModelPolicy{}, fmt.Errorf("Notion не сохранил доступность моделей (нужны права владельца/админа): %w", err)
	}
	policy := workspaceModelPolicy{found: true, disabledModels: disabledModels, disabledProviders: disabledProviders}
	catalog := make([]Model, 0, len(current.Models))
	for _, item := range current.Models {
		catalog = append(catalog, Model{ID: item.ID, Label: item.Label, Provider: item.Provider, Group: item.Group})
	}
	return workspacePolicyResult(cfg, mode, catalog, policy), nil
}
