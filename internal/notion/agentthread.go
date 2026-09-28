package notion

// Новый (агент-сервисный) протокол чата.
//
// Веб-клиент Notion больше не гоняет весь транскрипт через
// runInferenceTranscript. Новый путь такой:
//
//	1. POST /api/v3/createAgentThread          — первое сообщение + создание треда
//	2. POST /api/v3/sendEventToAgentThread     — каждое следующее сообщение
//	3. POST /api/v3/getThreadTranscript        — чтение ответа (патчи сущностей)
//
// Ответ здесь не стримится телом запроса: сервер копит патчи транскрипта, а
// клиент их вычитывает. Поэтому мы опрашиваем getThreadTranscript, проецируем
// сущности в те же части (text/thinking/tool_use), что и старый Accumulator,
// и отдаём в UI ровно те же Event'ы. Фронтенд об отличии протоколов не знает.
//
// Модель в этом протоколе не передаётся в теле сообщения: агент-сервис берёт
// её из настройки space_view.settings.personal_agent_model_preference, которую
// веб-клиент пишет через saveTransactionsFanout при выборе в пикере.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"neura/internal/uid"
)

const (
	warmAgentServicePath   = "/api/v3/warmAgentServiceChat"
	createAgentThreadPath  = "/api/v3/createAgentThread"
	sendAgentEventPath     = "/api/v3/sendEventToAgentThread"
	agentTranscriptPath    = "/api/v3/getThreadTranscript"
	transactionsFanoutPath = "/api/v3/saveTransactionsFanout"

	// Опрос транскрипта. Notion сам режет паузы между блоками, поэтому
	// секунда даёт живую «печать» без заметной нагрузки.
	agentPollInterval = 700 * time.Millisecond
	// Если ничего не меняется так долго — считаем ход зависшим.
	agentIdleTimeout = 15 * time.Minute
	// Сколько ждём, пока сессия вообще перейдёт в running после отправки.
	agentStartGrace = 20 * time.Second
)

// agentServicePaths — эндпоинты агент-сервиса, для которых набор
// заголовков урезается до того, что шлёт веб-клиент (см. client.go).
var agentServicePaths = map[string]bool{
	createAgentThreadPath:   true,
	sendAgentEventPath:      true,
	agentTranscriptPath:     true,
	createAgentUploadPath:   true,
	completeAgentUploadPath: true,
}

// agentServiceHeaders — белый список заголовков для этих путей.
// Всё остальное из импортированного cURL (accept: application/x-ndjson,
// x-notion-cell-hint чужого воркспейса, служебные x-notion-*) отбрасывается.
var agentServiceHeaders = map[string]bool{
	"cookie":                      true,
	"content-type":                true,
	"user-agent":                  true,
	"origin":                      true,
	"referer":                     true,
	"accept-language":             true,
	"notion-client-version":       true,
	"notion-audit-log-platform":   true,
	"x-notion-space-id":           true,
	"x-notion-active-user-header": true,
	// В рабочем HAR эта пара присутствует и указывает на shard того же
	// воркспейса. При смене space хинт удаляется (UseWorkspace,
	// ensureSpaceIdentity), чтобы не уехать в чужую cell.
	"x-notion-cell-hint":        true,
	"x-notion-cell-hint-source": true,
}

const (
	ModeLegacy = "legacy"
	ModeV2     = "v2"
	ModeV3     = "v3"
)

// NormalizeChatMode keeps old persisted aliases readable while exposing the
// actual wire protocols: workflow V2 and Agent Service V3 are both agent modes,
// but they use completely different transports.
func NormalizeChatMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "agent", "agent_service", "agent-service", ModeV3:
		return ModeV3
	case "workflow", "workflow_v2", "workflow-v2", ModeV2:
		return ModeV2
	case ModeLegacy:
		return ModeLegacy
	default:
		return ModeV2
	}
}

// AgentMode means the separate Agent Service V3 protocol.
func AgentMode(mode string) bool { return NormalizeChatMode(mode) == ModeV3 }

// WorkflowV2Mode means the agent-capable runInferenceTranscript protocol.
func WorkflowV2Mode(mode string) bool { return NormalizeChatMode(mode) == ModeV2 }

// agentPolicies повторяет веб-клиент: единственное подтверждённое значение —
// approval_mode: "yolo" (Skip all). Другие строки сервер отбрасывает с 400
// UserValidationError, поэтому при выключенном Skip all поле просто не
// отправляется и действует режим по умолчанию.
func agentPolicies(skipAll bool) map[string]interface{} {
	if skipAll {
		return map[string]interface{}{"approval_mode": "yolo"}
	}
	return nil
}

// agentMessageContent повторяет формат веб-клиента: rich-text и file_id.
func agentMessageContent(text string, attachments []Attachment) []interface{} {
	content := make([]interface{}, 0, 1+len(attachments))
	if text != "" {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": []interface{}{[]interface{}{text}},
		})
	}
	for _, attachment := range attachments {
		if id := strings.TrimSpace(attachment.AgentFileID); id != "" {
			content = append(content, map[string]interface{}{"type": "file", "file_id": id})
		}
	}
	return content
}

func agentTurnPayload(
	spaceID, threadID, clientEventID, newText string,
	content []interface{}, policies map[string]interface{}, mapped *userStep,
) map[string]interface{} {
	message := map[string]interface{}{"type": "user.message", "content": content}
	payload := map[string]interface{}{
		"spaceId": spaceID, "threadId": threadID,
		"surface": "full_page_chat", "policies": policies,
		"clientEventId": clientEventID,
	}
	if mapped != nil && mapped.Sequence > 0 && mapped.Content != newText {
		payload["events"] = []interface{}{
			map[string]interface{}{"type": "user.rewind", "rewind_to_sequence": mapped.Sequence},
			message,
		}
	} else {
		payload["event"] = message
	}
	if policies == nil {
		delete(payload, "policies")
	}
	return payload
}

func agentInterruptPayload(spaceID, threadID string) map[string]interface{} {
	return map[string]interface{}{
		"spaceId": spaceID, "threadId": threadID,
		"event":         map[string]interface{}{"type": "user.interrupt"},
		"clientEventId": uid.New(),
	}
}

// ensureSpaceIdentity сводит активного пользователя и space_view с воркспейсом.
//
// В cURL из DevTools может оказаться x-notion-active-user-header от другого
// аккаунта той же cookie-сессии (у Notion мультиаккаунт). Старому
// runInferenceTranscript это сходило с рук, а createAgentThread жёстко сверяет
// «личный агент этого юзера в этом спейсе» и падает с 400 UserValidationError.
// Поэтому перед созданием треда берём getSpaces и выбираем аккаунт, у
// которого есть space_view для нужного spaceId (гость без view не подходит).
func (c *Client) ensureSpaceIdentity(ctx context.Context, spaceID string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	spaceID = strings.TrimSpace(spaceID)
	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	if spaceID == "" {
		return errors.New("не выбран воркспейс")
	}

	accounts, err := c.Accounts(ctx)
	if err != nil {
		// Не валим ход из-за справочного запроса: пробуем как есть.
		return nil
	}

	type identityCandidate struct {
		userID string
		viewID string
		rank   int
	}
	candidates := []identityCandidate{}
	for _, account := range accounts {
		for _, space := range account.Spaces {
			if space.ID != spaceID || strings.TrimSpace(space.SpaceViewID) == "" {
				continue
			}
			rank := 1
			if account.UserID == cfg.UserID {
				rank = 2
			}
			// Один workspace может быть открыт несколькими аккаунтами одной
			// browser-сессии, но Personal Agent rollout включён не каждому.
			// Проверяем модельный endpoint именно от имени кандидата и ставим
			// agent-capable аккаунт выше просто «текущего» из импортированного cURL.
			probeCtx := withHeaderOverrides(ctx, map[string]string{
				"x-notion-active-user-header": account.UserID,
				"x-notion-space-id":           spaceID,
			}, true)
			if available, probeErr := c.PostJSON(probeCtx, "/api/v3/getAvailableModels", map[string]interface{}{"spaceId": spaceID}); probeErr == nil {
				models, _ := available["models"].([]interface{})
				restricted, _ := available["modelSelectionRestricted"].(bool)
				if len(models) > 0 {
					rank = 5
				} else if !restricted {
					rank = 4
				}
			}
			candidates = append(candidates, identityCandidate{userID: account.UserID, viewID: space.SpaceViewID, rank: rank})
		}
	}
	if len(candidates) == 0 {
		return errors.New("ни у одного из аккаунтов сессии нет доступа к этому воркспейсу")
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.rank > best.rank {
			best = candidate
		}
	}
	userID, viewID := best.userID, best.viewID

	c.mu.Lock()
	defer c.mu.Unlock()
	live := c.cfg
	if live == nil {
		return errors.New("сначала подключите Notion в настройках")
	}
	if live.SpaceID != spaceID {
		// Хинт привязан к старому воркспейсу — без него Notion маршрутизирует по spaceId.
		delete(live.Headers, "x-notion-cell-hint")
		delete(live.Headers, "x-notion-cell-hint-source")
	}
	live.UserID = userID
	live.SpaceID = spaceID
	live.SpaceViewID = viewID
	setHeader(live.Headers, "x-notion-active-user-header", userID)
	setHeader(live.Headers, "x-notion-space-id", spaceID)
	return nil
}

// setHeader заменяет заголовок без оглядки на регистр исходного ключа.
// В cURL из DevTools имена бывают в любом регистре, а дубль ключей
// в map давал случайный итоговый заголовок.
func setHeader(headers map[string]string, name, value string) {
	if headers == nil {
		return
	}
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
	headers[name] = value
}

// setModelPreference переключает модель личного агента так же, как пикер в
// веб-клиенте: запись в settings нужного space_view.
func (r *Runtime) setModelPreference(ctx context.Context, cfg *Config, model, effort string) error {
	model = strings.TrimSpace(model)
	spaceViewID := strings.TrimSpace(cfg.SpaceViewID)
	if spaceViewID == "" {
		return errors.New("не известен spaceViewId: переимпортируйте cURL")
	}
	// Пустая модель означает «Авто». Важно не делать no-op: иначе после
	// ручного выбора в space_view остаётся старая закреплённая модель.
	var preference interface{}
	if model != "" {
		selected := map[string]interface{}{"model": model}
		if value := strings.TrimSpace(effort); value != "" {
			selected["reasoning_effort"] = value
		}
		preference = selected
	}
	_, err := r.client.PostJSON(ctx, transactionsFanoutPath, map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{map[string]interface{}{
			"id":      uid.New(),
			"spaceId": cfg.SpaceID,
			"debug": map[string]interface{}{
				"userAction":         "PersonalAgentModelPreference.set",
				"clientCommitTimeMs": time.Now().UnixMilli(),
			},
			"operations": []interface{}{map[string]interface{}{
				"pointer": map[string]interface{}{
					"table": "space_view", "id": spaceViewID, "spaceId": cfg.SpaceID,
				},
				"path":    []interface{}{"settings"},
				"command": "update",
				"args":    map[string]interface{}{"personal_agent_model_preference": preference},
			}},
		}},
	})
	return err
}

// knownModels — известные кодовые имена моделей. Этот каталог никогда не
// показывается целиком: Models фильтрует его политикой текущего workspace
// отдельно для legacy и agent_service.
func knownModels() []Model {
	type entry struct{ id, label, provider string }
	catalog := []entry{
		{"agave-flan", "Opus 5", "anthropic"},
		{"ambrosia-tart-high", "Opus 4.8", "anthropic"},
		{"apricot-sorbet-high", "Opus 4.7", "anthropic"},
		{"avocado-froyo-medium", "Opus 4.6", "anthropic"},
		{"angel-cake-high", "Sonnet 5", "anthropic"},
		{"almond-croissant-low", "Sonnet 4.6", "anthropic"},
		{"anthropic-haiku-4.5", "Haiku 4.5", "anthropic"},
		{"assam-chai", "Fable 5.1", "anthropic"},
		{"acai-budino-high", "Fable 5", "anthropic"},
		{"orlando-quinn", "GPT-6 Astra", "openai"},
		{"orange-mousse", "GPT-5.6 Sol", "openai"},
		{"orchid-muffin", "GPT-5.6 Terra", "openai"},
		{"olive-jellyroll", "GPT-5.6 Luna", "openai"},
		{"opal-quince-medium", "GPT-5.5", "openai"},
		{"oval-kumquat-medium", "GPT-5.4", "openai"},
		{"oregon-grape-medium", "GPT-5.4 Mini", "openai"},
		{"otaheite-apple-medium", "GPT-5.4 Nano", "openai"},
		{"oatmeal-cookie", "GPT-5.2", "openai"},
		{"soursop-shortcake", "Grok 4.6", "xai"},
		{"strawberry-whoopiepie", "Grok 4.5", "xai"},
		{"xigua-mochi-medium", "Grok 4.3", "xai"},
		{"xinomavro-cake", "Grok Build 0.1", "xai"},
		{"grapefruit-zeppole", "Gemini 3.7 Flash", "gemini"},
		{"vertex-gemini-3.6-flash", "Gemini 3.6 Flash", "gemini"},
		{"vertex-gemini-3.5-flash", "Gemini 3.5 Flash", "gemini"},
		{"galette-medium-thinking", "Gemini 3.1 Pro", "gemini"},
		{"gingerbread", "Gemini 3 Flash", "gemini"},
		{"fireworks-kimi-k3", "Kimi K3", "kimi"},
		{"fireworks-kimi-k2.7", "Kimi K2.7", "kimi"},
		{"fireworks-kimi-k2.6", "Kimi K2.6", "kimi"},
		{"baseten-deepseek-v4-pro", "DeepSeek V4 Pro", "deepseek"},
		{"baseten-deepseek-v4-flash", "DeepSeek V4 Flash", "deepseek"},
		{"baseten-glm-5.2", "GLM 5.2", "glm"},
	}
	out := make([]Model, 0, len(catalog))
	for _, item := range catalog {
		out = append(out, Model{
			ID: item.id, Label: item.label, Provider: item.provider,
			Group: "Доступно в воркспейсе",
		})
	}
	return out
}

// streamAgent проводит один ход нового протокола и отдаёт события UI.
func (r *Runtime) streamAgent(ctx context.Context, cfg *Config, req ChatRequest, emit func(Event)) error {
	var userMessages []Message
	for _, message := range req.Messages {
		if message.Role == "user" {
			userMessages = append(userMessages, message)
		}
	}
	if len(userMessages) == 0 {
		return errors.New("пустое сообщение")
	}
	latest := userMessages[len(userMessages)-1]
	text := strings.TrimSpace(latest.Content)
	if text == "" && len(latest.Attachments) == 0 {
		return errors.New("пустое сообщение")
	}

	ctx, cancel := context.WithCancel(ctx)
	runID := uid.New()
	r.mu.Lock()
	previous, hadPrevious := r.cancels[req.ConversationID]
	r.cancels[req.ConversationID] = activeRun{id: runID, cancel: cancel}
	convo := r.state(req.ConversationID, cfg.SpaceID, ModeV3)
	threadID, spaceID, started := convo.ThreadID, convo.SpaceID, convo.Started
	index := len(userMessages) - 1
	var mapped *userStep
	if index < len(convo.UserSteps) {
		copy := convo.UserSteps[index]
		mapped = &copy
	}
	attachments := latest.Attachments
	if mapped != nil && len(attachments) == 0 {
		attachments = mapped.Attachments
	}
	save := r.threadSave
	r.mu.Unlock()
	if hadPrevious {
		previous.cancel()
	}
	defer func() {
		cancel()
		r.mu.Lock()
		if current, ok := r.cancels[req.ConversationID]; ok && current.id == runID {
			delete(r.cancels, req.ConversationID)
		}
		r.mu.Unlock()
	}()
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
	}

	if !started {
		// V3 browser capture warms Agent Service before the first turn. The
		// endpoint is an optimization, so a rollout-specific failure must not
		// block createAgentThread.
		_, _ = r.client.PostJSON(ctx, warmAgentServicePath, map[string]interface{}{"spaceId": spaceID})
		if err := r.client.ensureSpaceIdentity(ctx, spaceID); err != nil {
			return err
		}
		if fresh, err := r.client.require(); err == nil {
			cfg = fresh
			if strings.TrimSpace(cfg.SpaceID) != "" {
				spaceID = cfg.SpaceID
			}
		}
	}
	if known, present := sessionHasUser(cfg.Headers, cfg.UserID); known && !present {
		return fmt.Errorf(
			"выбранный аккаунт %s отсутствует в импортированной browser-сессии: "+
				"в Notion переключитесь на него, откройте AI-чат и заново импортируйте Copy as cURL",
			cfg.UserID,
		)
	}
	if err := r.setModelPreference(ctx, cfg, req.Model, req.ReasoningEffort); err != nil {
		return fmt.Errorf("не удалось применить выбранную модель: %w", err)
	}

	body := text
	if prompt := strings.TrimSpace(req.SystemPrompt); prompt != "" && !started {
		body = prompt + "\n\n" + text
	}
	content := agentMessageContent(body, attachments)
	if len(content) == 0 {
		return errors.New("вложения не загружены в agent service")
	}
	policies := agentPolicies(req.SkipApprovals)
	clientMessageID := uid.New()

	if !started {
		payload := map[string]interface{}{
			"type":                      "personal_agent",
			"spaceId":                   spaceID,
			"threadId":                  threadID,
			"enableSuggestedEditsTools": req.SuggestedEdits,
			"createdSource":             "full_page_chat",
			"content":                   content,
			"policies":                  policies,
			"agentMemorySettings": map[string]interface{}{
				"useMemories":             req.UseMemories,
				"excludeChatFromMemories": req.ExcludeFromMemories,
			},
			"browserEnabled":  req.BrowserEnabled,
			"clientMessageId": clientMessageID,
		}
		if policies == nil {
			delete(payload, "policies")
		}
		response, err := r.createAgentThread(ctx, payload)
		if err != nil {
			return err
		}
		if id, _ := response["threadId"].(string); strings.TrimSpace(id) != "" {
			threadID = id
		}
		r.mu.Lock()
		convo.ThreadID = threadID
		convo.Started = true
		r.mu.Unlock()
		if save != nil {
			if err := save(spaceID, req.ConversationID, ModeV3, threadID); err != nil {
				return fmt.Errorf("не удалось сохранить привязку agent-чата: %w", err)
			}
		}
		if title := agentThreadTitle(response); title != "" {
			r.mu.Lock()
			convo.Title = title
			r.mu.Unlock()
			emit(Event{Type: "thread-title", Title: title})
		}
	} else {
		payload := agentTurnPayload(
			spaceID, threadID, clientMessageID, latest.Content, content, policies, mapped,
		)
		if _, err := r.client.PostJSON(ctx, sendAgentEventPath, payload); err != nil {
			return err
		}
	}

	sequence, err := r.followAgentThread(ctx, spaceID, threadID, clientMessageID, emit)
	r.mu.Lock()
	if index < len(convo.UserSteps) {
		convo.UserSteps = convo.UserSteps[:index]
	}
	convo.UserSteps = append(convo.UserSteps, userStep{
		LocalID: latest.ID, Content: latest.Content, Sequence: sequence, Attachments: attachments,
	})
	r.mu.Unlock()
	return err
}

type agentAttempt struct {
	name    string
	headers map[string]string
	full    bool
}

// createAgentThread последовательно пробует варианты, пока сервер не ответит 2xx.
func (r *Runtime) createAgentThread(
	ctx context.Context, payload map[string]interface{},
) (map[string]interface{}, error) {
	r.mu.Lock()
	pinned := r.agentVariant
	r.mu.Unlock()

	// Рабочий browser fetch использует текущую buildVersion (из импортированного
	// cURL) и Referer /ai. Ранее здесь насильно ставилась старая ...0853, поэтому
	// тот же payload из Go получал 400, хотя в консоли браузера проходил с ...1117.
	cfg := r.client.Config()
	referer := strings.TrimRight(cfg.Origin, "/") + "/ai"
	agentAttempts := []agentAttempt{
		{
			name: "точные заголовки успешного browser cURL",
			headers: map[string]string{
				"referer": referer,
			},
			full: true,
		},
		{
			name: "без notion-client-version",
			headers: map[string]string{
				"referer":               referer,
				"notion-client-version": "",
			},
		},
	}

	order := make([]int, 0, len(agentAttempts))
	if pinned >= 1 && pinned <= len(agentAttempts) {
		order = append(order, pinned-1)
	}
	for index := range agentAttempts {
		if len(order) > 0 && order[0] == index {
			continue
		}
		order = append(order, index)
	}

	var lastErr error
	tried := make([]string, 0, len(order))
	for _, index := range order {
		attempt := agentAttempts[index]
		requestCtx := withHeaderOverrides(ctx, attempt.headers, attempt.full)
		response, err := r.client.PostJSON(requestCtx, createAgentThreadPath, payload)
		if err == nil {
			r.mu.Lock()
			r.agentVariant = index + 1
			r.mu.Unlock()
			return response, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		// Перебирать имеет смысл только на валидации; 401/403/5xx — другая беда.
		if !strings.Contains(err.Error(), " 400 ") {
			return nil, err
		}
		lastErr = err
		tried = append(tried, attempt.name)
	}
	return nil, fmt.Errorf(
		"createAgentThread отклонён во всех вариантах (%s) для активного Notion-аккаунта %s. "+
			"Payload, client version и Referer совпадают с рабочим browser fetch; наиболее вероятно, импортированная cookie-сессия устарела. "+
			"Скопируйте как cURL именно последний успешный createAgentThread из браузера и импортируйте его заново. Последний ответ: %w",
		strings.Join(tried, "; "), r.client.Config().UserID, lastErr,
	)
}

// followAgentThread вычитывает транскрипт до конца хода.
func (r *Runtime) followAgentThread(ctx context.Context, spaceID, threadID, clientMessageID string, emit func(Event)) (int64, error) {
	projector := &agentProjector{expectedClientMessageID: clientMessageID}
	start := time.Now()
	lastChange := time.Now()
	lastPing := time.Now()
	sawWork := false

	for {
		if ctx.Err() != nil {
			emit(Event{Type: "done"})
			return projector.turnStart, nil
		}
		page, err := r.client.PostJSON(ctx, agentTranscriptPath, map[string]interface{}{
			"spaceId":   spaceID,
			"threadId":  threadID,
			"direction": "backward",
			// Веб-клиент берёт ровно 10; большие значения могут не пройти валидацию.
			"limit": 10,
		})
		if err != nil {
			if ctx.Err() != nil {
				emit(Event{Type: "done"})
				return projector.turnStart, nil
			}
			return projector.turnStart, err
		}

		status, events := projector.apply(page)
		for _, event := range events {
			emit(event)
			lastChange = time.Now()
			lastPing = time.Now()
		}
		switch status {
		case "running", "pending", "starting", "waiting":
			sawWork = true
		}
		if agentTurnFinished(status) && (projector.turnFound || sawWork || time.Since(start) > agentStartGrace) {
			break
		}
		if time.Since(lastChange) > agentIdleTimeout {
			break
		}
		if time.Since(lastPing) > 10*time.Second {
			// UI считает поток мёртвым, если долго нет событий.
			emit(Event{Type: "ping"})
			lastPing = time.Now()
		}

		select {
		case <-ctx.Done():
			emit(Event{Type: "done"})
			return projector.turnStart, nil
		case <-time.After(agentPollInterval):
		}
	}

	emit(Event{Type: "done"})
	return projector.turnStart, nil
}

func (r *Runtime) agentThreadState(
	ctx context.Context, spaceID, conversationID, threadID string, maxUsers int,
) (ThreadState, error) {
	state := ThreadState{ThreadID: threadID}
	if strings.TrimSpace(threadID) == "" {
		return state, nil
	}
	projector := &agentProjector{}
	cursor := ""
	status := ""
	for pageIndex := 0; pageIndex < 100; pageIndex++ {
		request := map[string]interface{}{
			"spaceId": spaceID, "threadId": threadID, "direction": "backward", "limit": 10,
		}
		if cursor != "" {
			request["cursor"] = cursor
		}
		page, err := r.client.PostJSON(ctx, agentTranscriptPath, request)
		if err != nil {
			return state, err
		}
		if current, _ := projector.apply(page); current != "" {
			status = current
		}
		hasMore, _ := page["has_more_backward"].(bool)
		cursor, _ = page["backward_cursor"].(string)
		users := 0
		for _, entity := range projector.entities {
			if entity.kind == "user_message" {
				users++
			}
		}
		if !hasMore || cursor == "" {
			break
		}
		if maxUsers > 0 && users >= maxUsers {
			state.HasEarlier = true
			break
		}
	}
	state.Running = status == "running" || status == "pending" || status == "starting" || status == "waiting"
	state.Found = len(projector.entities) > 0

	list := make([]agentEntity, 0, len(projector.entities))
	for _, entity := range projector.entities {
		list = append(list, entity)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].sequence < list[j].sequence })
	base := time.Now().UnixMilli() - int64(len(list)+1)
	userSteps := make([]userStep, 0)
	segment := map[string]agentEntity{}
	segmentID := ""
	segmentAt := int64(0)
	flushAssistant := func() {
		if len(segment) == 0 {
			return
		}
		parts := (&agentProjector{entities: segment}).project()
		turn := ThreadTurn{ID: segmentID + "-a", Role: "assistant", CreatedAt: segmentAt}
		for _, item := range parts {
			switch item.kind {
			case "text":
				turn.Parts = append(turn.Parts, ThreadPart{Kind: "text", Text: item.text})
				turn.Content += item.text
			case "thinking":
				turn.Parts = append(turn.Parts, ThreadPart{Kind: "thought", Text: item.text})
			case "tool_use":
				turn.Parts = append(turn.Parts, ThreadPart{
					Kind: "tool", ID: item.id, Name: item.name, Server: item.server,
					Args: item.args, Result: item.result, Done: item.done,
				})
			}
		}
		if len(turn.Parts) > 0 {
			state.Turns = append(state.Turns, turn)
		}
		segment = map[string]agentEntity{}
		segmentID, segmentAt = "", 0
	}
	for index, entity := range list {
		id, _ := entity.data["id"].(string)
		createdAt := agentEntityCreatedAt(entity.data)
		if createdAt == 0 {
			createdAt = base + int64(index)
		}
		if entity.kind != "user_message" {
			segment[id] = entity
			if segmentID == "" {
				segmentID, segmentAt = id, createdAt
			}
			continue
		}
		flushAssistant()
		text := agentEntityText(entity.data)
		attachments := make([]Attachment, 0)
		for _, piece := range agentContentParts(entity.data) {
			kind, _ := piece["type"].(string)
			if kind == "file" {
				if fileID, _ := stringField(piece, "file_id", "fileId"); fileID != "" {
					attachments = append(attachments, Attachment{AgentFileID: fileID, StepID: fileID, StepType: "agent-file"})
				}
				continue
			}
			if kind == "text" && text == "" {
				text += agentRichText(piece["text"])
			}
		}
		userParts := []ThreadPart{{Kind: "text", Text: text}}
		for index := range attachments {
			attachment := attachments[index]
			userParts = append(userParts, ThreadPart{Kind: "attachment", Attachment: &attachment})
		}
		state.Turns = append(state.Turns, ThreadTurn{
			ID: id, Role: "user", Content: text, CreatedAt: createdAt, Parts: userParts,
		})
		userSteps = append(userSteps, userStep{
			NotionID: id, Content: text, Sequence: entity.sequence, Attachments: attachments,
		})
	}
	flushAssistant()

	if state.Found {
		r.mu.Lock()
		convo := r.state(conversationID, spaceID, ModeV3)
		convo.ThreadID, convo.Started, convo.UserSteps = threadID, true, userSteps
		r.mu.Unlock()
	}
	return state, nil
}

func agentEntityCreatedAt(entity map[string]interface{}) int64 {
	for _, key := range []string{"created_at", "createdAt", "timestamp"} {
		if value := int64Number(entity[key]); value > 0 {
			if value < 1e12 {
				value *= 1000
			}
			return value
		}
		if value, ok := entity[key].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				return parsed.UnixMilli()
			}
		}
	}
	return 0
}

func agentTurnFinished(status string) bool {
	switch status {
	case "idle", "completed", "complete", "stopped", "cancelled", "canceled", "error", "failed":
		return true
	}
	return false
}

func agentThreadTitle(response map[string]interface{}) string {
	thread, ok := response["thread"].(map[string]interface{})
	if !ok {
		return ""
	}
	data, ok := thread["data"].(map[string]interface{})
	if !ok {
		return ""
	}
	title, _ := data["title"].(string)
	return strings.TrimSpace(title)
}

// stopAgentThread повторяет подтверждённое HAR-событие кнопки Stop.
func (r *Runtime) stopAgentThread(ctx context.Context, spaceID, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return nil
	}
	_, err := r.client.PostJSON(ctx, sendAgentEventPath, agentInterruptPayload(spaceID, threadID))
	return err
}

// ---------------------------------------------------------------- проекция

type agentEntity struct {
	sequence int64
	kind     string
	data     map[string]interface{}
}

// agentProjector держит сущности транскрипта и превращает их в те же части,
// что и Accumulator старого протокола, чтобы diff/события были идентичны.
type agentProjector struct {
	entities                map[string]agentEntity
	prev                    []part
	expectedClientMessageID string
	turnStart               int64
	turnFound               bool
}

func (p *agentProjector) apply(page map[string]interface{}) (string, []Event) {
	if p.entities == nil {
		p.entities = map[string]agentEntity{}
	}
	status := ""
	patches, _ := page["patches"].([]interface{})
	for _, raw := range patches {
		patch, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch op, _ := patch["op"].(string); op {
		case "put", "upsert", "update":
			entity, ok := patch["entity"].(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := entity["id"].(string)
			if strings.TrimSpace(id) == "" {
				continue
			}
			kind, _ := entity["kind"].(string)
			p.entities[id] = agentEntity{
				sequence: int64Number(entity["sequence"]),
				kind:     kind,
				data:     entity,
			}
			if kind == "user_message" {
				clientID, _ := stringField(entity, "client_message_id", "clientMessageId")
				if p.expectedClientMessageID == "" || clientID == p.expectedClientMessageID {
					sequence := int64Number(entity["sequence"])
					if !p.turnFound || sequence >= p.turnStart {
						p.turnStart = sequence
						p.turnFound = true
					}
				}
			}
		case "patch":
			id, _ := patch["id"].(string)
			entity, exists := p.entities[id]
			if !exists {
				continue
			}
			ops, _ := patch["ops"].([]interface{})
			applyAgentEntityPatch(entity.data, ops)
			p.entities[id] = entity
		case "remove", "delete":
			if id, _ := patch["id"].(string); id != "" {
				delete(p.entities, id)
			}
		case "session":
			if session, ok := patch["session"].(map[string]interface{}); ok {
				if value, _ := session["status"].(string); value != "" {
					status = value
				}
			}
		}
	}
	// Итоговый статус страницы важнее промежуточных патчей.
	if session, ok := page["session"].(map[string]interface{}); ok {
		if value, _ := session["status"].(string); value != "" {
			status = value
		}
	}
	return status, p.diff(p.project())
}

// applyAgentEntityPatch применяет компактные JSON-patch обновления из
// getThreadTranscript. В HAR результат инструмента приходит именно как
// {op:"patch", id:"tool:...", ops:[{op:"add", path:"/result", ...}]}.
func applyAgentEntityPatch(entity map[string]interface{}, ops []interface{}) {
	for _, raw := range ops {
		op, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := op["path"].(string)
		segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if path == "" || len(segments) == 0 {
			continue
		}
		for index := range segments {
			segments[index] = strings.ReplaceAll(strings.ReplaceAll(segments[index], "~1", "/"), "~0", "~")
		}
		parent := entity
		for _, segment := range segments[:len(segments)-1] {
			next, exists := parent[segment].(map[string]interface{})
			if !exists {
				next = map[string]interface{}{}
				parent[segment] = next
			}
			parent = next
		}
		key := segments[len(segments)-1]
		switch action, _ := op["op"].(string); action {
		case "add", "replace":
			parent[key] = op["value"]
		case "remove":
			delete(parent, key)
		}
	}
}

func (p *agentProjector) project() []part {
	// backward limit=10 возвращает историю нескольких завершённых ходов. Пока
	// сервер не подтвердил именно отправленное clientMessageId, ничего не
	// эмитим — иначе старые ответы склеиваются с новым сообщением.
	if p.expectedClientMessageID != "" && !p.turnFound {
		return nil
	}
	list := make([]agentEntity, 0, len(p.entities))
	for _, item := range p.entities {
		list = append(list, item)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].sequence < list[j].sequence })

	var parts []part
	results := map[string]interface{}{}
	seenTools := map[string]struct{}{}

	for _, item := range list {
		if p.turnFound && item.sequence <= p.turnStart {
			continue
		}
		id, _ := item.data["id"].(string)
		kind := item.kind
		switch {
		case kind == "user_message", kind == "turn_completed", kind == "turn_started", kind == "":
			// Реплику пользователя UI уже нарисовал сам, служебное пропускаем.
		case kind == "thinking" || kind == "reasoning":
			text := agentEntityText(item.data)
			if text == "" {
				continue
			}
			parts = append(parts, part{kind: "thinking", key: "e:" + id, text: text})
		case kind == "assistant_message" || kind == "agent_message":
			for index, piece := range agentContentParts(item.data) {
				text, _ := piece["text"].(string)
				if text == "" {
					text, _ = piece["content"].(string)
				}
				if strings.TrimSpace(text) == "" {
					continue
				}
				key := "e:" + id + ":" + strconv.Itoa(index)
				if pieceKind, _ := piece["type"].(string); pieceKind == "thinking" {
					parts = append(parts, part{kind: "thinking", key: key, text: text})
					continue
				}
				parts = append(parts, part{kind: "text", key: key, text: text})
			}
			if len(agentContentParts(item.data)) == 0 {
				if text := agentEntityText(item.data); text != "" {
					parts = append(parts, part{kind: "text", key: "e:" + id, text: text})
				}
			}
		case strings.Contains(kind, "tool_result"), strings.Contains(kind, "tool_output"):
			target, _ := stringField(item.data, "tool_use_id", "toolUseId", "tool_call_id", "toolCallId", "call_id")
			if target == "" {
				target = id
			}
			if value, ok := toolResultPayload(item.data); ok {
				results[target] = value
			}
		case strings.Contains(kind, "tool"):
			callID, _ := stringField(item.data, "tool_use_id", "toolUseId", "tool_call_id", "toolCallId", "call_id", "id")
			if callID == "" {
				callID = id
			}
			if _, ok := seenTools[callID]; ok {
				if value, ok := toolResultPayload(item.data); ok {
					results[callID] = value
				}
				continue
			}
			seenTools[callID] = struct{}{}
			name, _ := stringField(item.data, "tool_name", "toolName", "name")
			server, _ := stringField(item.data, "server_name", "serverName", "module_name", "moduleName", "integrationName")
			result, done := toolResultPayload(item.data)
			if display := agentToolDisplayText(item.data, done); display != "" && (name == "" || name == "mcp_run_tool") {
				name = display
				if server == "" {
					if pieces := strings.SplitN(display, " / ", 2); len(pieces) == 2 {
						server, name = strings.TrimSpace(pieces[0]), strings.TrimSpace(pieces[1])
					}
				}
			}
			parts = append(parts, part{
				kind: "tool_use", key: "tool:" + callID, id: callID,
				name: name, server: server, args: extractToolArgs(item.data),
			})
			if done {
				results[callID] = result
			}
		}
	}

	for i := range parts {
		if parts[i].kind != "tool_use" {
			continue
		}
		if value, ok := results[parts[i].id]; ok {
			parts[i].result = value
			parts[i].done = true
		}
	}
	return parts
}

// diff повторяет логику Accumulator.diff: наружу уходит только прирост.
func (p *agentProjector) diff(next []part) []Event {
	seen := make(map[string]part, len(p.prev))
	for _, item := range p.prev {
		seen[item.key] = item
	}
	var events []Event
	for _, current := range next {
		before := seen[current.key]
		switch current.kind {
		case "text", "thinking":
			delta := addedTail(before.text, current.text)
			if delta == "" {
				continue
			}
			if current.kind == "text" {
				events = append(events, Event{Type: "text-delta", Delta: delta})
			} else {
				events = append(events, Event{Type: "reasoning-delta", Delta: delta})
			}
		case "tool_use":
			if before.kind != "tool_use" {
				events = append(events, Event{
					Type: "tool-call", ID: current.id, Name: current.name,
					Server: current.server, Args: current.args,
				})
			} else if !sameJSON(before.args, current.args) {
				events = append(events, Event{
					Type: "tool-args", ID: current.id, Name: current.name,
					Server: current.server, Args: current.args,
				})
			}
			if current.done && !before.done {
				events = append(events, Event{
					Type: "tool-result", ID: current.id, Name: current.name, Result: current.result,
				})
			}
		}
	}
	p.prev = mergeParts(p.prev, next)
	return events
}

func agentToolDisplayText(entity map[string]interface{}, done bool) string {
	labels, _ := entity["text"].(map[string]interface{})
	if labels == nil {
		return ""
	}
	state := "running"
	if done {
		state = "finished"
	}
	return strings.TrimSpace(agentRichText(labels[state]))
}

func agentRichText(value interface{}) string {
	var sb strings.Builder
	var walk func(interface{})
	walk = func(current interface{}) {
		switch typed := current.(type) {
		case string:
			sb.WriteString(typed)
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	return sb.String()
}

// agentEntityText достаёт текст сущности во всех замеченных формах.
func agentEntityText(entity map[string]interface{}) string {
	if value, ok := stringField(entity, "content_text", "contentText", "text_content", "summary"); ok {
		return value
	}
	switch value := entity["text"].(type) {
	case string:
		return value
	case []interface{}:
		// rich-text вида [["строка"]]
		return agentRichText(value)
	}
	if value, ok := entity["content"].(string); ok {
		return value
	}
	return ""
}

func agentContentParts(entity map[string]interface{}) []map[string]interface{} {
	list, ok := entity["content"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, raw := range list {
		if item, ok := raw.(map[string]interface{}); ok {
			out = append(out, item)
		}
	}
	return out
}
