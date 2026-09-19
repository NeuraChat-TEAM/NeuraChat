package notion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"neura/internal/uid"
)

const inferencePath = "/api/v3/runInferenceTranscript"

// Attachment is an uploaded file already registered with Notion.
type Attachment struct {
	StepID      string                 `json:"stepId"`
	StepType    string                 `json:"stepType"`
	FileURL     string                 `json:"fileUrl"`
	SignedGet   string                 `json:"signedGetUrl"`
	FileName    string                 `json:"fileName"`
	ContentType string                 `json:"contentType"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// Message is one chat turn coming from the UI.
type Message struct {
	ID          string       `json:"id"`
	Role        string       `json:"role"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// ChatRequest is the payload the frontend sends for every send/regenerate.
type ChatRequest struct {
	ConversationID  string `json:"conversationId"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	SystemPrompt    string `json:"systemPrompt"`
	// SearchAllSources = «All sources I can access». По умолчанию выключено:
	// агент не шарит по всему воркспейсу, пока это явно не разрешили.
	SearchAllSources bool      `json:"searchAllSources"`
	Messages         []Message `json:"messages"`
}

// conversation keeps the Notion-side identity of a local chat.
type conversation struct {
	ThreadID       string
	SpaceID        string
	Title          string
	Workflow       map[string]interface{}
	InitialContext map[string]interface{}
	MessageIDs     []string
	Started        bool
	UserSteps      []userStep
	LastUpdated    time.Time
	// Pending — последний запрос агента на ввод (ask-survey и другие
	// user.input_request): без него ответ на опросник некуда адресовать.
	Pending pendingInput
}

type userStep struct {
	LocalID  string
	NotionID string
	Content  string
}

// Runtime owns conversation state and the live inference cancellations.
type Runtime struct {
	client *Client

	mu        sync.Mutex
	convos    map[string]*conversation
	cancels   map[string]context.CancelFunc
	convoTTL  time.Duration
	lastSweep time.Time

	// Связка «локальный чат → thread в Notion» живёт в SQLite, иначе после
	// рестарта приложения каждый старый чат продолжался как новый thread.
	threadLoad func(spaceID, conversationID string) string
	threadSave func(spaceID, conversationID, threadID string)
}

// SetThreadStore wires durable storage for the local↔Notion thread mapping.
func (r *Runtime) SetThreadStore(
	load func(spaceID, conversationID string) string,
	save func(spaceID, conversationID, threadID string),
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threadLoad, r.threadSave = load, save
}

func NewRuntime(client *Client) *Runtime {
	return &Runtime{
		client:   client,
		convos:   map[string]*conversation{},
		cancels:  map[string]context.CancelFunc{},
		convoTTL: 12 * time.Hour,
	}
}

func (r *Runtime) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.convos = map[string]*conversation{}
}

// sweep drops stale conversations. The Node sidecar leaked these forever.
func (r *Runtime) sweep() {
	if time.Since(r.lastSweep) < 10*time.Minute {
		return
	}
	r.lastSweep = time.Now()
	for id, convo := range r.convos {
		if time.Since(convo.LastUpdated) > r.convoTTL {
			delete(r.convos, id)
		}
	}
}

func (r *Runtime) state(conversationID, spaceID string) *conversation {
	r.sweep()
	convo, ok := r.convos[conversationID]
	if !ok {
		convo = &conversation{ThreadID: uid.New(), SpaceID: spaceID}
		// Если этот чат уже имел thread в этом же воркспейсе — продолжаем его.
		if r.threadLoad != nil {
			if saved := strings.TrimSpace(r.threadLoad(spaceID, conversationID)); saved != "" {
				convo.ThreadID = saved
				convo.Started = true
			}
		}
		r.convos[conversationID] = convo
	}
	convo.LastUpdated = time.Now()
	return convo
}

func (r *Runtime) threadPointer(convo *conversation) map[string]interface{} {
	return map[string]interface{}{"table": "thread", "id": convo.ThreadID, "spaceId": convo.SpaceID}
}

// Stream replays one inference and pushes UI events into emit.
func (r *Runtime) Stream(ctx context.Context, req ChatRequest, emit func(Event)) error {
	cfg, err := r.client.require()
	if err != nil {
		return err
	}
	if req.ConversationID == "" {
		req.ConversationID = uid.New()
	}

	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if previous, ok := r.cancels[req.ConversationID]; ok {
		previous()
	}
	r.cancels[req.ConversationID] = cancel
	body, convo, edit, err := r.buildBody(cfg, req)
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.cancels, req.ConversationID)
		r.mu.Unlock()
	}()
	if err != nil {
		return err
	}

	if edit != nil {
		if err := r.applyMessageEdit(ctx, convo, edit); err != nil {
			return err
		}
	}

	resp, debug, err := r.client.Post(ctx, inferencePath, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw := make([]byte, 2048)
		n, _ := resp.Body.Read(raw)
		r.client.appendDebug(debug, raw[:n])
		err := errors.New("Notion вернул " + resp.Status + ": " + strings.TrimSpace(string(raw[:n])))
		r.client.finishDebug(debug, err)
		return err
	}

	r.mu.Lock()
	convo.Started = true
	save, threadID, spaceID := r.threadSave, convo.ThreadID, convo.SpaceID
	r.mu.Unlock()
	if save != nil {
		save(spaceID, req.ConversationID, threadID)
	}

	accumulator := NewAccumulator()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)

	// Долгие ответы: Notion иногда молчит десятки секунд между блоками, и UI
	// успевал решить, что поток умер. Шлём ping, пока стрим жив; emit при этом
	// вызывается из двух горутин, поэтому сериализуем его мьютексом.
	var emitMu sync.Mutex
	safeEmit := func(event Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(event)
	}
	streamDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-streamDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				safeEmit(Event{Type: "ping"})
			}
		}
	}()
	defer close(streamDone)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		r.client.appendDebug(debug, []byte(line+"\n"))

		var frame map[string]interface{}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		for _, event := range r.handleFrame(convo, accumulator, frame) {
			safeEmit(event)
		}
	}
	if err := scanner.Err(); err != nil {
		r.client.finishDebug(debug, err)
		return err
	}

	r.client.finishDebug(debug, nil)
	safeEmit(Event{Type: "done"})
	return nil
}

func (r *Runtime) handleFrame(convo *conversation, acc *Accumulator, frame map[string]interface{}) []Event {
	// Любой кадр может принести interaction_id/request_sequence — запоминаем
	// их, чтобы ответ опросника ушёл в тот же ход агента.
	var found pendingInput
	notePendingInput(frame, &found, 0)
	if found.InteractionID != "" || found.Sequence > 0 || found.ToolName != "" {
		r.mu.Lock()
		if found.InteractionID != "" {
			convo.Pending.InteractionID = found.InteractionID
		}
		if found.Sequence > 0 {
			convo.Pending.Sequence = found.Sequence
		}
		if found.ToolName != "" {
			convo.Pending.ToolName = found.ToolName
		}
		r.mu.Unlock()
	}

	kind, _ := frame["type"].(string)
	switch kind {
	case "patch-start":
		data, _ := frame["data"].(map[string]interface{})
		return acc.Reset(data["s"])
	case "patch-sync":
		// Раньше здесь шёл transcript-reset: UI выбрасывал весь ответ и
		// начинал проявлять его заново на каждой синхронизации. Аккумулятор
		// теперь сам сравнивает снимок с уже показанным и отдаёт только хвост.
		data, _ := frame["data"].(map[string]interface{})
		return acc.Reset(data["s"])
	case "patch":
		return acc.Apply(frame["v"])
	case "record-map":
		if title := r.absorbRecordMap(convo, frame["recordMap"]); title != "" {
			return []Event{{Type: "thread-title", Title: title}}
		}
	case "error":
		message, _ := frame["message"].(string)
		if message == "" {
			message = "Notion вернул ошибку в потоке"
		}
		return []Event{{Type: "error", Delta: message, Message: message, Label: message}}
	}
	return nil
}

func (r *Runtime) absorbRecordMap(convo *conversation, raw interface{}) string {
	recordMap, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var discovered string
	if threads, ok := recordMap["thread"].(map[string]interface{}); ok {
		if stored := recordValue(threads[convo.ThreadID]); stored != nil {
			if ids, ok := stored["messages"].([]interface{}); ok {
				convo.MessageIDs = convo.MessageIDs[:0]
				for _, id := range ids {
					if value, ok := id.(string); ok {
						convo.MessageIDs = append(convo.MessageIDs, value)
					}
				}
			}
			if data, ok := stored["data"].(map[string]interface{}); ok {
				if title, ok := data["title"].(string); ok {
					if trimmed := strings.TrimSpace(title); trimmed != "" && trimmed != convo.Title {
						convo.Title = trimmed
						discovered = trimmed
					}
				}
			}
		}
	}
	if messages, ok := recordMap["thread_message"].(map[string]interface{}); ok {
		for _, record := range messages {
			stored := recordValue(record)
			if stored == nil {
				continue
			}
			step, ok := stored["step"].(map[string]interface{})
			if !ok {
				continue
			}
			if kind, _ := step["type"].(string); kind == "config" || kind == "workflow" {
				convo.Workflow = step
				break
			}
		}
	}
	return discovered
}

func recordValue(record interface{}) map[string]interface{} {
	envelope, ok := record.(map[string]interface{})
	if !ok {
		return nil
	}
	inner, ok := envelope["value"].(map[string]interface{})
	if !ok {
		return nil
	}
	stored, ok := inner["value"].(map[string]interface{})
	if !ok {
		return nil
	}
	return stored
}

type messageEdit struct {
	UserStepID string
	RemoveIDs  []string
	Content    string
}

// buildBody rebuilds the transcript the same way the web client does.
// Caller holds r.mu.
func (r *Runtime) buildBody(cfg *Config, req ChatRequest) (map[string]interface{}, *conversation, *messageEdit, error) {
	var userMessages []Message
	for _, message := range req.Messages {
		if message.Role == "user" {
			userMessages = append(userMessages, message)
		}
	}
	if len(userMessages) == 0 {
		return nil, nil, nil, errors.New("пустое сообщение")
	}
	latest := userMessages[len(userMessages)-1]
	if strings.TrimSpace(latest.Content) == "" && len(latest.Attachments) == 0 {
		return nil, nil, nil, errors.New("пустое сообщение")
	}

	convo := r.state(req.ConversationID, cfg.SpaceID)
	hasThread := convo.Started

	body := cloneMap(cfg.Template)
	transcript, _ := body["transcript"].([]interface{})

	workflow := convo.Workflow
	if workflow == nil {
		workflow = firstStepOfType(transcript, "workflow", "config")
	}
	contextStep := lastStepOfType(transcript, "context")
	userTemplate := lastStepOfType(transcript, "user")

	steps := make([]interface{}, 0, 8)

	if workflow != nil {
		step := cloneMap(workflow)
		if id, _ := step["id"].(string); id == "" || !hasThread {
			step["id"] = uid.New()
		}
		if value, ok := step["value"].(map[string]interface{}); ok {
			// modelFromUser tells Notion the picker was used explicitly.
			if req.Model != "" {
				value["model"] = req.Model
				value["modelFromUser"] = true
			}
			if req.ReasoningEffort != "" {
				value["reasoningEffort"] = req.ReasoningEffort
			}
			// По умолчанию скоуп поиска пустой (аналог выключенного
			// «All sources I can access» в веб-клиенте).
			if req.SearchAllSources {
				value["searchScopes"] = []interface{}{map[string]interface{}{"type": "everything"}}
			} else {
				value["searchScopes"] = []interface{}{}
			}
			// The system prompt rides along as additional instructions so it is
			// always the first thing the model reads.
			if prompt := strings.TrimSpace(req.SystemPrompt); prompt != "" {
				value["customInstructions"] = prompt
				value["systemPrompt"] = prompt
			}
		}
		convo.Workflow = cloneMap(step)
		steps = append(steps, step)
	}

	if hasThread && convo.InitialContext != nil {
		steps = append(steps, cloneMap(convo.InitialContext))
	}
	if contextStep != nil {
		step := cloneMap(contextStep)
		step["id"] = uid.New()
		if value, ok := step["value"].(map[string]interface{}); ok {
			value["currentDatetime"] = time.Now().Format(time.RFC3339)
			if hasThread {
				value["surface"] = "full_page_chat"
			} else {
				value["surface"] = "ai_module"
			}
		}
		if !hasThread {
			convo.InitialContext = cloneMap(step)
		}
		steps = append(steps, step)
	}

	for _, attachment := range latest.Attachments {
		if attachment.StepID == "" || attachment.FileURL == "" {
			continue
		}
		kind := "computer-file"
		if attachment.StepType == "attachment" {
			kind = "attachment"
		}
		steps = append(steps, map[string]interface{}{
			"id": attachment.StepID, "type": kind,
			"fileUrl": attachment.FileURL, "fileName": attachment.FileName,
			"contentType": attachment.ContentType, "metadata": attachment.Metadata,
		})
	}
	if hasThread {
		steps = append(steps, map[string]interface{}{"id": uid.New(), "type": "updated-config"})
	}

	index := len(userMessages) - 1
	var mapped *userStep
	if index < len(convo.UserSteps) {
		mapped = &convo.UserSteps[index]
	}
	reuse := hasThread && mapped != nil
	isEdit := reuse && mapped.Content != latest.Content

	notionUserID := uid.New()
	if reuse {
		notionUserID = mapped.NotionID
	}

	userStepValue := map[string]interface{}{}
	if userTemplate != nil {
		userStepValue = cloneMap(userTemplate)
	}
	userStepValue["id"] = notionUserID
	userStepValue["type"] = "user"
	userStepValue["value"] = []interface{}{[]interface{}{latest.Content}}
	userStepValue["createdAt"] = time.Now().Format(time.RFC3339)
	steps = append(steps, userStepValue)

	var edit *messageEdit
	if isEdit {
		edit = &messageEdit{UserStepID: notionUserID, Content: latest.Content}
		for i, id := range convo.MessageIDs {
			if id == notionUserID {
				edit.RemoveIDs = append(edit.RemoveIDs, convo.MessageIDs[i+1:]...)
				break
			}
		}
	}

	if index < len(convo.UserSteps) {
		convo.UserSteps = convo.UserSteps[:index]
	}
	convo.UserSteps = append(convo.UserSteps, userStep{LocalID: latest.ID, NotionID: notionUserID, Content: latest.Content})

	body["threadId"] = convo.ThreadID
	body["traceId"] = uid.New()
	body["createThread"] = !hasThread
	body["generateTitle"] = !hasThread || (isEdit && index == 0)
	body["saveAllThreadOperations"] = true
	body["setUnreadState"] = true
	body["asPatchResponse"] = true
	body["patchResponseVersion"] = 2
	body["isPartialTranscript"] = hasThread
	body["transcript"] = steps

	// Fields the web client always sends; keep imported values when present.
	for key, fallback := range map[string]interface{}{
		"threadType":                             "workflow",
		"createdSource":                          "ai_module",
		"supportsCustomAgentNudgeTranscriptStep": true,
		"isUserInAnySalesAssistedSpace":          false,
		"isSpaceSalesAssisted":                   false,
		"debugOverrides": map[string]interface{}{
			"emitAgentSearchExtractedResults": true,
			"cachedInferences":                map[string]interface{}{},
			"annotationInferences":            map[string]interface{}{},
			"emitInferences":                  false,
		},
	} {
		if _, ok := body[key]; !ok {
			body[key] = fallback
		}
	}
	if hasThread {
		delete(body, "threadParentPointer")
	} else if _, ok := body["threadParentPointer"]; !ok {
		return nil, nil, nil, errors.New("в cURL нет threadParentPointer")
	}

	return body, convo, edit, nil
}

func (r *Runtime) applyMessageEdit(ctx context.Context, convo *conversation, edit *messageEdit) error {
	operations := []interface{}{
		map[string]interface{}{
			"pointer": map[string]interface{}{"table": "thread_message", "id": edit.UserStepID, "spaceId": convo.SpaceID},
			"path":    []interface{}{"step"},
			"command": "update",
			"args":    map[string]interface{}{"value": []interface{}{[]interface{}{edit.Content}}},
		},
	}
	for _, id := range edit.RemoveIDs {
		operations = append(operations, map[string]interface{}{
			"pointer": r.threadPointer(convo),
			"path":    []interface{}{"messages"},
			"command": "listRemove",
			"args":    map[string]interface{}{"id": id},
		})
	}
	_, err := r.client.PostJSON(ctx, "/api/v3/saveTransactionsFanout", map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{map[string]interface{}{
			"id":         uid.New(),
			"spaceId":    convo.SpaceID,
			"debug":      map[string]interface{}{"userAction": "AgentUserStep.saveUserStepChanges", "clientCommitTimeMs": time.Now().UnixMilli()},
			"operations": operations,
		}},
	})
	return err
}

// Stop clears the current inference lease and cancels the local stream.
func (r *Runtime) Stop(ctx context.Context, conversationID string) error {
	r.mu.Lock()
	cancel := r.cancels[conversationID]
	convo := r.convos[conversationID]
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if convo == nil {
		return nil
	}
	_, err := r.client.PostJSON(ctx, "/api/v3/saveTransactionsFanout", map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{map[string]interface{}{
			"id":      uid.New(),
			"spaceId": convo.SpaceID,
			"debug":   map[string]interface{}{"userAction": "AgentChatTranscript.StopInference.stopButtonClick", "clientCommitTimeMs": time.Now().UnixMilli()},
			"operations": []interface{}{map[string]interface{}{
				"pointer": r.threadPointer(convo),
				"path":    []interface{}{},
				"command": "update",
				"args":    map[string]interface{}{"current_inference_id": nil, "current_inference_lease_expiration": nil},
			}},
		}},
	})
	return err
}

// Model is a trimmed entry from getAvailableModels.
type Model struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Provider         string   `json:"provider"`
	Group            string   `json:"group"`
	ReasoningEfforts []string `json:"reasoningEfforts"`
	DefaultEffort    string   `json:"defaultReasoningEffort"`
}

func (r *Runtime) Models(ctx context.Context) ([]Model, error) {
	cfg, err := r.client.require()
	if err != nil {
		return nil, err
	}
	payload, err := r.client.PostJSON(ctx, "/api/v3/getAvailableModels", map[string]interface{}{"spaceId": cfg.SpaceID})
	if err != nil {
		return nil, err
	}
	raw, _ := payload["models"].([]interface{})
	out := make([]Model, 0, len(raw))
	for _, item := range raw {
		model, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if disabled, _ := model["isDisabled"].(bool); disabled {
			continue
		}
		if restricted, _ := model["restrictedForPersonalAgent"].(bool); restricted {
			continue
		}
		workflow, _ := model["workflow"].(map[string]interface{})
		if workflow == nil {
			continue
		}
		if disabled, _ := workflow["isDisabled"].(bool); disabled {
			continue
		}
		if name, _ := workflow["finalModelName"].(string); name == "" {
			continue
		}
		id, _ := model["model"].(string)
		label, _ := model["modelMessage"].(string)
		if label == "" {
			label = id
		}
		provider, _ := model["modelProvider"].(string)
		group, _ := model["displayGroup"].(string)

		entry := Model{ID: id, Label: label, Provider: provider, Group: group}
		if configuration, ok := model["modelConfiguration"].(map[string]interface{}); ok {
			if efforts, ok := configuration["supportedReasoningEfforts"].([]interface{}); ok {
				for _, effort := range efforts {
					if value, ok := effort.(string); ok {
						entry.ReasoningEfforts = append(entry.ReasoningEfforts, value)
					}
				}
			}
			entry.DefaultEffort, _ = configuration["defaultReasoningEffort"].(string)
		}
		out = append(out, entry)
	}
	return out, nil
}

func firstStepOfType(transcript []interface{}, kinds ...string) map[string]interface{} {
	for _, raw := range transcript {
		step, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := step["type"].(string)
		for _, want := range kinds {
			if kind == want {
				return step
			}
		}
	}
	return nil
}

// cloneMap deep-copies a decoded JSON object.
func cloneMap(source map[string]interface{}) map[string]interface{} {
	raw, err := json.Marshal(source)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}
