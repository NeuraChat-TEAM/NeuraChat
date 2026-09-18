package notion

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"neura/internal/uid"
)

// Все пути ниже сняты с живого HAR-трейса веб-клиента Notion
// (подключение MCP-сервера через ngrok), поэтому ничего не угадываем.
const (
	PathTransactions = "/api/v3/saveTransactionsFanout"

	pathOAuthSupport  = "/api/v3/checkMcpOAuthSupport"
	pathValidate      = "/api/v3/validateMcpConnection"
	pathConnect       = "/api/v3/postWorkflowsMcpServerConnect"
	pathModuleSetting = "/api/v3/updateMcpServerModuleSettings"
	pathDisconnect    = "/api/v3/disconnectPersonalMcpServerModule"
	pathSyncRecords   = "/api/v3/syncRecordValuesMain"
)

// mcpSteps — то, что показываем в настройках: шаг -> реальный путь.
var mcpSteps = map[string]string{
	"validate":   pathValidate,
	"connect":    pathConnect,
	"settings":   pathModuleSetting,
	"disconnect": pathDisconnect,
}

var (
	mcpPathMu        sync.RWMutex
	mcpPathOverrides = map[string]string{}
)

// SetMcpPathOverrides остаётся для ручного переопределения, если Notion
// когда-нибудь переименует приватный эндпоинт.
func SetMcpPathOverrides(paths map[string]string) {
	mcpPathMu.Lock()
	defer mcpPathMu.Unlock()
	mcpPathOverrides = map[string]string{}
	for step, path := range paths {
		if p := normalizeMcpPath(path); p != "" {
			mcpPathOverrides[step] = p
		}
	}
}

// ResolvedMcpPaths показывает, какой путь используется на каждом шаге.
func ResolvedMcpPaths() map[string]string {
	mcpPathMu.RLock()
	defer mcpPathMu.RUnlock()
	out := map[string]string{}
	for step, def := range mcpSteps {
		if override := mcpPathOverrides[step]; override != "" {
			out[step] = override + " (вручную)"
			continue
		}
		out[step] = def
	}
	return out
}

func (c *Client) mcpPath(step, def string) string {
	mcpPathMu.RLock()
	defer mcpPathMu.RUnlock()
	if override := mcpPathOverrides[step]; override != "" {
		return override
	}
	return def
}

// normalizeMcpPath принимает имя, путь или полный URL.
func normalizeMcpPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "/api/v3/"); idx >= 0 {
		value = value[idx:]
	}
	if !strings.HasPrefix(value, "/") {
		value = "/api/v3/" + value
	}
	if q := strings.IndexAny(value, "?#"); q >= 0 {
		value = value[:q]
	}
	return value
}

// McpTool — один инструмент MCP-сервера.
type McpTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// McpModule — подключённый MCP-сервер в терминах Notion.
type McpModule struct {
	IntegrationID string    `json:"integrationId"`
	Name          string    `json:"name"`
	OfficialName  string    `json:"officialName,omitempty"`
	ServerURL     string    `json:"serverUrl"`
	Enabled       bool      `json:"enabled"`
	ToolCount     int       `json:"toolCount"`
	Tools         []McpTool `json:"tools,omitempty"`
	// SettingsWarning — необязательный шаг настроек не прошёл, но сервер подключён.
	SettingsWarning string `json:"settingsWarning,omitempty"`
}

// ConnectMcpInput описывает сервер, который надо подключить к Notion.
type ConnectMcpInput struct {
	Name      string `json:"name"`
	ServerURL string `json:"serverUrl"`
	Token     string `json:"token"`
	AutoWrite bool   `json:"runWriteToolsAutomatically"`

	// Headers — дополнительные заголовки авторизации (X-API-Key и прочее).
	// Query — параметры строки запроса. Smithery и подобные хостинги
	// не читают Authorization, а ждут ?api_key=...&profile=... и без них
	// сразу отвечают отказом — именно это и было в ошибке browserbase.
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`

	// AutoRun — сразу разрешить все инструменты (run automatically).
	AutoRun bool `json:"autoRun,omitempty"`
}

// applyQuery доклеивает параметры авторизации к адресу сервера.
func applyQuery(serverURL string, query map[string]string) string {
	serverURL = strings.TrimSpace(serverURL)
	if len(query) == 0 || serverURL == "" {
		return serverURL
	}
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return serverURL
	}
	values := parsed.Query()
	for key, value := range query {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		values.Set(key, value)
	}
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

// authHeadersFor собирает Authorization и произвольные заголовки в один список.
// Порядок стабилен, иначе Notion при повторном подключении видит другой набор.
func (c *Client) authHeadersFor(token string, headers map[string]string) []interface{} {
	out := []interface{}{}
	if value := strings.TrimSpace(token); value != "" {
		out = append(out, map[string]interface{}{
			"name":  "Authorization",
			"value": "Bearer " + value,
		})
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		value := strings.TrimSpace(headers[name])
		if value == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "authorization") && strings.TrimSpace(token) != "" {
			continue // уже добавлен из токена
		}
		out = append(out, map[string]interface{}{
			"name":  strings.TrimSpace(name),
			"value": value,
		})
	}
	return out
}

func (c *Client) authHeaders(token string) []interface{} {
	return c.authHeadersFor(token, nil)
}

// CheckMcpOAuth спрашивает Notion, требует ли сервер OAuth. Для notcode с
// Bearer-токеном ответ игнорируем, но ошибка сети здесь — сразу понятный сигнал.
func (c *Client) CheckMcpOAuth(ctx context.Context, serverURL string) (map[string]interface{}, error) {
	return c.PostJSON(ctx, pathOAuthSupport, map[string]interface{}{"serverUrl": serverURL})
}

// ValidateMcp просит Notion самому дойти до сервера и перечислить инструменты.
// Это первый шаг веб-клиента и лучший источник понятной ошибки.
func (c *Client) ValidateMcp(ctx context.Context, serverURL, token string) ([]McpTool, string, error) {
	return c.ValidateMcpWith(ctx, serverURL, token, nil)
}

// ValidateMcpWith — та же проверка, но с произвольными заголовками (X-API-Key
// и прочее). Smithery/Context7 без них отвечают отказом.
func (c *Client) ValidateMcpWith(ctx context.Context, serverURL, token string, headers map[string]string) ([]McpTool, string, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, "", err
	}
	out, err := c.PostJSON(ctx, c.mcpPath("validate", pathValidate), map[string]interface{}{
		"serverUrl":         serverURL,
		"spaceId":           cfg.SpaceID,
		"authHeaders":       c.authHeadersFor(token, headers),
		"initiationContext": "connect",
	})
	if err != nil {
		return nil, "", describeMcpError(err)
	}
	if success, ok := out["success"].(bool); ok && !success {
		return nil, "", fmt.Errorf("Notion не смог подключиться к %s: %s", serverURL, mcpErrorText(out))
	}
	official, _ := out["officialName"].(string)
	return parseTools(out), official, nil
}

// describeMcpError переводит сырой ответ Notion в понятный текст.
// Сообщение про disabled часто является побочным 403 при несовпадении
// active user / space / space_view, поэтому не обвиняем настройки workspace.
func describeMcpError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "custom mcp servers are disabled"):
		return errors.New("Notion вернул 403 при регистрации MCP. Подробный ответ сохранён в диагностике")
	case strings.Contains(lower, "forbiddenerror") || strings.Contains(text, "403"):
		return fmt.Errorf("Notion отказал в доступе (403). Проверьте права в воркспейсе и свежесть сессии: %s", text)
	case strings.Contains(lower, "unauthorized") || strings.Contains(text, "401"):
		return fmt.Errorf("Notion не авторизовал запрос (401) — переимпортируйте cURL сессии: %s", text)
	}
	return err
}

func mcpErrorText(payload map[string]interface{}) string {
	for _, key := range []string{"error", "message", "errorMessage", "reason"} {
		if text, ok := payload[key].(string); ok && text != "" {
			return text
		}
	}
	if list, ok := payload["validationErrors"].([]interface{}); ok && len(list) > 0 {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, "; ")
	}
	return "сервер ответил отказом"
}

// ConnectMcp повторяет цепочку из HAR веб-клиента:
// checkMcpOAuthSupport -> validateMcpConnection -> postWorkflowsMcpServerConnect
// -> saveTransactionsFanout(space_view.settings.agent_chat_modules).
func (c *Client) ConnectMcp(ctx context.Context, in ConnectMcpInput) (*McpModule, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	serverURL := strings.TrimSpace(in.ServerURL)
	if serverURL == "" {
		return nil, errors.New("не указан адрес MCP-сервера")
	}
	// Параметры строки запроса вшиваем в адрес: Notion не умеет их передавать
	// отдельно, а Smithery без api_key/profile сразу отвечает отказом.
	serverURL = applyQuery(serverURL, in.Query)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "notcode"
	}

	// 1. Discovery OAuth — отдельный первый запрос веб-клиента. Он не заменяет
	// Bearer-валидацию, но ловит невалидный URL до регистрации модуля.
	if _, err := c.CheckMcpOAuth(ctx, serverURL); err != nil {
		return nil, describeMcpError(err)
	}

	// 2. Проверка: Notion сам стучится на сервер и получает список тулзов.
	tools, official, err := c.ValidateMcpWith(ctx, serverURL, in.Token, in.Headers)
	if err != nil {
		return nil, err
	}

	// 3. Создание workflow_module. integrationId генерирует клиент.
	integrationID := uid.New()
	// Точная форма moduleDefinition из HAR: флаги автозапуска сюда не входят.
	moduleDefinition := map[string]interface{}{"name": name, "serverUrl": serverURL}
	created, err := c.PostJSON(ctx, c.mcpPath("connect", pathConnect), map[string]interface{}{
		"integrationId":     integrationID,
		"spaceId":           cfg.SpaceID,
		"authHeaders":       c.authHeadersFor(in.Token, in.Headers),
		"moduleDefinition":  moduleDefinition,
		"initiationContext": "connect",
	})
	if err != nil {
		return nil, describeMcpError(err)
	}
	if success, ok := created["success"].(bool); ok && !success {
		return nil, fmt.Errorf("Notion отклонил подключение: %s", mcpErrorText(created))
	}
	if list := parseTools(created); len(list) > 0 {
		tools = list
	}
	if text, _ := created["officialName"].(string); text != "" {
		official = text
	}
	if id, ok := stringField(created, "moduleId", "integrationId"); ok && id != "" {
		integrationID = id
	}

	module := &McpModule{
		IntegrationID: integrationID,
		Name:          name,
		OfficialName:  official,
		ServerURL:     serverURL,
		Enabled:       true,
		ToolCount:     len(tools),
		Tools:         tools,
	}

	// 4. HAR после connect добавляет pointer workflow_module в
	// space_view.settings.agent_chat_modules через saveTransactionsFanout.
	// Без этого модуль установлен, но не появляется в текущем AI-чате.
	if err := c.addAgentChatModuleWith(ctx, cfg.SpaceViewID, integrationID, in.AutoRun, toolNames(tools)); err != nil {
		module.Enabled = false
		return module, fmt.Errorf("MCP зарегистрирован, но не добавлен в текущий чат: %w", err)
	}
	return module, nil
}

func parseTools(payload map[string]interface{}) []McpTool {
	var raw []interface{}
	for _, key := range []string{"tools", "discoveredTools", "toolDefinitions"} {
		if list, ok := payload[key].([]interface{}); ok {
			raw = list
			break
		}
	}
	out := make([]McpTool, 0, len(raw))
	for _, item := range raw {
		tool, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		title, _ := tool["title"].(string)
		description, _ := tool["description"].(string)
		out = append(out, McpTool{Name: name, Title: title, Description: description})
	}
	return out
}

// spaceViewSettings читает текущие настройки space_view целиком: их нельзя
// перезаписывать по частям, веб-клиент тоже шлёт объект целиком.
func (c *Client) spaceViewSettings(ctx context.Context, spaceViewID string) (map[string]interface{}, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	out, err := c.PostJSON(ctx, pathSyncRecords, map[string]interface{}{
		"requests": []interface{}{map[string]interface{}{
			"pointer": map[string]interface{}{
				"table": "space_view", "id": spaceViewID, "spaceId": cfg.SpaceID,
			},
			"version": -1,
		}},
		"spacePointer": map[string]interface{}{"table": "space", "id": cfg.SpaceID},
	})
	if err != nil {
		return nil, err
	}
	recordMap, _ := out["recordMap"].(map[string]interface{})
	views, _ := recordMap["space_view"].(map[string]interface{})
	record, _ := views[spaceViewID].(map[string]interface{})
	value, _ := record["value"].(map[string]interface{})
	if inner, ok := value["value"].(map[string]interface{}); ok {
		value = inner
	}
	settings, _ := value["settings"].(map[string]interface{})
	if settings == nil {
		settings = map[string]interface{}{}
	}
	return settings, nil
}

func (c *Client) moduleEntries(settings map[string]interface{}) []interface{} {
	if list, ok := settings["agent_chat_modules"].([]interface{}); ok {
		return list
	}
	return []interface{}{}
}

func entryModuleID(entry interface{}) string {
	item, ok := entry.(map[string]interface{})
	if !ok {
		return ""
	}
	pointer, _ := item["pointer"].(map[string]interface{})
	id, _ := pointer["id"].(string)
	return id
}

// writeSpaceViewSettings отправляет транзакцию update на путь ["settings"] —
// точно так, как это делает agentPersistenceHelpers.addAgentChatModule.
func (c *Client) writeSpaceViewSettings(ctx context.Context, spaceViewID string, settings map[string]interface{}, userAction string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	_, err = c.PostJSON(ctx, PathTransactions, map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{map[string]interface{}{
			"id":      uid.New(),
			"spaceId": cfg.SpaceID,
			"debug": map[string]interface{}{
				"userAction":         userAction,
				"clientCommitTimeMs": time.Now().UnixMilli(),
			},
			"operations": []interface{}{map[string]interface{}{
				"pointer": map[string]interface{}{
					"table": "space_view", "id": spaceViewID, "spaceId": cfg.SpaceID,
				},
				"path":    []interface{}{"settings"},
				"command": "update",
				"args":    settings,
			}},
		}},
	})
	return err
}

// toolNames — плоский список имён инструментов для разрешения автозапуска.
func toolNames(tools []McpTool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func (c *Client) addAgentChatModule(ctx context.Context, spaceViewID, moduleID string) error {
	return c.addAgentChatModuleWith(ctx, spaceViewID, moduleID, false, nil)
}

// addAgentChatModuleWith добавляет модуль в чат и, если нужно, сразу разрешает
// все его инструменты (аналог «run automatically» в веб-клиенте).
func (c *Client) addAgentChatModuleWith(ctx context.Context, spaceViewID, moduleID string, autoRun bool, tools []string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if spaceViewID == "" {
		spaceViewID = cfg.SpaceViewID
	}
	if spaceViewID == "" {
		return errors.New("не известен space_view id — переимпортируйте cURL")
	}
	settings, err := c.spaceViewSettings(ctx, spaceViewID)
	if err != nil {
		return err
	}
	entries := c.moduleEntries(settings)
	kept := make([]interface{}, 0, len(entries)+1)
	for _, entry := range entries {
		if entryModuleID(entry) != moduleID {
			kept = append(kept, entry)
		}
	}
	entry := map[string]interface{}{
		"pointer": map[string]interface{}{
			"table": "workflow_module", "id": moduleID, "spaceId": cfg.SpaceID,
		},
		"defaultEnabled": true,
	}
	if autoRun {
		// Разрешаем выполнять инструменты без ручного подтверждения.
		entry["runWriteToolsAutomatically"] = true
		if len(tools) > 0 {
			allowed := make([]interface{}, 0, len(tools))
			for _, name := range tools {
				allowed = append(allowed, name)
			}
			entry["allowedTools"] = allowed
		}
	}
	kept = append(kept, entry)
	settings["agent_chat_modules"] = kept
	return c.writeSpaceViewSettings(ctx, spaceViewID, settings, "agentPersistenceHelpers.addAgentChatModule")
}

// ListMcp собирает подключённые MCP-модули: сначала пойнтеры из space_view,
// затем сами записи workflow_module.
func (c *Client) ListMcp(ctx context.Context) ([]McpModule, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	spaceViewID := cfg.SpaceViewID
	if spaceViewID == "" {
		return nil, errors.New("не известен space_view id — переимпортируйте cURL")
	}
	settings, err := c.spaceViewSettings(ctx, spaceViewID)
	if err != nil {
		return nil, err
	}
	entries := c.moduleEntries(settings)
	if len(entries) == 0 {
		return []McpModule{}, nil
	}

	enabled := map[string]bool{}
	requests := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		id := entryModuleID(entry)
		if id == "" {
			continue
		}
		if item, ok := entry.(map[string]interface{}); ok {
			flag, _ := item["defaultEnabled"].(bool)
			enabled[id] = flag
		}
		requests = append(requests, map[string]interface{}{
			"pointer": map[string]interface{}{
				"table": "workflow_module", "id": id, "spaceId": cfg.SpaceID,
			},
			"version": -1,
		})
	}
	if len(requests) == 0 {
		return []McpModule{}, nil
	}

	out, err := c.PostJSON(ctx, pathSyncRecords, map[string]interface{}{
		"requests":     requests,
		"spacePointer": map[string]interface{}{"table": "space", "id": cfg.SpaceID},
	})
	if err != nil {
		return nil, err
	}
	recordMap, _ := out["recordMap"].(map[string]interface{})
	modules, _ := recordMap["workflow_module"].(map[string]interface{})

	result := make([]McpModule, 0, len(modules))
	for id, item := range modules {
		record, _ := item.(map[string]interface{})
		value, _ := record["value"].(map[string]interface{})
		if inner, ok := value["value"].(map[string]interface{}); ok {
			value = inner
		}
		if alive, ok := value["alive"].(bool); ok && !alive {
			continue
		}
		data, _ := value["data"].(map[string]interface{})
		if data == nil {
			continue
		}
		serverURL, _ := data["serverUrl"].(string)
		if serverURL == "" {
			continue // не MCP-модуль
		}
		name, _ := data["name"].(string)
		official, _ := data["officialName"].(string)
		tools := parseTools(data)
		result = append(result, McpModule{
			IntegrationID: id,
			Name:          name,
			OfficialName:  official,
			ServerURL:     serverURL,
			Enabled:       enabled[id],
			ToolCount:     len(tools),
			Tools:         tools,
		})
	}
	return result, nil
}

// SetMcpEnabled перезаписывает список включённых в чате модулей.
func (c *Client) SetMcpEnabled(ctx context.Context, spaceViewID string, modules []McpModule) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if spaceViewID == "" {
		spaceViewID = cfg.SpaceViewID
	}
	if spaceViewID == "" {
		return errors.New("не известен space_view id (переимпортируйте cURL)")
	}
	settings, err := c.spaceViewSettings(ctx, spaceViewID)
	if err != nil {
		return err
	}
	entries := make([]interface{}, 0, len(modules))
	for _, module := range modules {
		if module.IntegrationID == "" {
			continue
		}
		entries = append(entries, map[string]interface{}{
			"pointer": map[string]interface{}{
				"table": "workflow_module", "id": module.IntegrationID, "spaceId": cfg.SpaceID,
			},
			"defaultEnabled": module.Enabled,
		})
	}
	settings["agent_chat_modules"] = entries
	return c.writeSpaceViewSettings(ctx, spaceViewID, settings, "agentPersistenceHelpers.addAgentChatModule")
}

// DisconnectMcp отключает модуль так же, как кнопка «Disconnect» в Notion,
// и убирает его из списка модулей чата.
func (c *Client) DisconnectMcp(ctx context.Context, integrationID string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if integrationID == "" {
		return errors.New("не указан модуль")
	}
	if _, err := c.PostJSON(ctx, c.mcpPath("disconnect", pathDisconnect), map[string]interface{}{
		"spaceId":     cfg.SpaceID,
		"spaceViewId": cfg.SpaceViewID,
		"moduleId":    integrationID,
	}); err != nil {
		return err
	}

	if cfg.SpaceViewID == "" {
		return nil
	}
	settings, err := c.spaceViewSettings(ctx, cfg.SpaceViewID)
	if err != nil {
		return nil // модуль уже отключён, список подтянется при следующем чтении
	}
	entries := c.moduleEntries(settings)
	kept := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		if entryModuleID(entry) != integrationID {
			kept = append(kept, entry)
		}
	}
	if len(kept) == len(entries) {
		return nil
	}
	settings["agent_chat_modules"] = kept
	return c.writeSpaceViewSettings(ctx, cfg.SpaceViewID, settings, "agentPersistenceHelpers.removeAgentChatModule")
}
