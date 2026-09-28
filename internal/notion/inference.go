package notion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	AgentFileID string                 `json:"agentFileId,omitempty"`
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

	// Mode = legacy, v2 (agent workflow over runInferenceTranscript), or
	// v3 (createAgentThread / sendEventToAgentThread Agent Service).
	Mode string `json:"mode"`
	// BrowserEnabled — разрешить агенту веб/браузерный движок.
	BrowserEnabled bool `json:"browserEnabled"`
	// SkipApprovals = approval_mode "yolo": инструменты без подтверждений.
	SkipApprovals bool `json:"skipApprovals"`
	// UseMemories / ExcludeFromMemories = agentMemorySettings.
	UseMemories         bool `json:"useMemories"`
	ExcludeFromMemories bool `json:"excludeChatFromMemories"`
	// SuggestedEdits = enableSuggestedEditsTools.
	SuggestedEdits bool `json:"suggestedEdits"`
}

// conversation keeps the Notion-side identity of a local chat.
type conversation struct {
	ThreadID       string
	SpaceID        string
	Mode           string
	Title          string
	Workflow       map[string]interface{}
	InitialContext map[string]interface{}
	MessageIDs     []string
	Started        bool
	// AgentThread — тред создан новым протоколом (createAgentThread).
	// Старый и новый треды несовместимы, поэтому при смене режима внутри
	// одного чата создаётся новый тред.
	AgentThread bool
	UserSteps   []userStep
	LastUpdated time.Time
	// Pending — последний запрос агента на ввод (ask-survey и другие
	// user.input_request): без него ответ на опросник некуда адресовать.
	Pending pendingInput
}

type userStep struct {
	LocalID     string
	NotionID    string
	Content     string
	Sequence    int64
	Attachments []Attachment
}

type activeRun struct {
	id     string
	cancel context.CancelFunc
}

// Runtime owns conversation state and the live inference cancellations.
type Runtime struct {
	client *Client

	mu        sync.Mutex
	convos    map[string]*conversation
	cancels   map[string]activeRun
	convoTTL  time.Duration
	lastSweep time.Time

	// busy — чат занят отправкой, но собственного стрима ещё (или уже) нет:
	// загрузка вложений, сборка тела запроса, доигрывание плавного вывода.
	// Пока счётчик положительный, сверка с Notion не имеет права заменять
	// локальную историю: снапшот записей может быть старше нашего сообщения.
	busy map[string]int
	// threadVersions — последняя увиденная версия записи thread. Веб-клиент
	// шлёт version в syncRecordValuesSpaceInitial и игнорирует устаревшие
	// снапшоты; мы делаем то же самое вручную.
	threadVersions map[string]int64

	// Связка «локальный чат → thread в Notion» живёт в SQLite, иначе после
	// рестарта приложения каждый старый чат продолжался как новый thread.
	threadLoad func(spaceID, conversationID, mode string) string
	threadSave func(spaceID, conversationID, mode, threadID string) error

	// agentVariant — номер схемы запроса createAgentThread, которая уже
	// сработала в этой сессии (1-based, 0 = ещё не подобрана).
	agentVariant int
}

// SetThreadStore wires durable storage for the local↔Notion thread mapping.
func (r *Runtime) SetThreadStore(
	load func(spaceID, conversationID, mode string) string,
	save func(spaceID, conversationID, mode, threadID string) error,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threadLoad, r.threadSave = load, save
}

func NewRuntime(client *Client) *Runtime {
	return &Runtime{
		client:         client,
		convos:         map[string]*conversation{},
		cancels:        map[string]activeRun{},
		busy:           map[string]int{},
		threadVersions: map[string]int64{},
		convoTTL:       12 * time.Hour,
	}
}

func (r *Runtime) Reset() {
	r.mu.Lock()
	runs := make([]activeRun, 0, len(r.cancels))
	for _, run := range r.cancels {
		runs = append(runs, run)
	}
	r.convos = map[string]*conversation{}
	r.cancels = map[string]activeRun{}
	r.busy = map[string]int{}
	r.threadVersions = map[string]int64{}
	r.agentVariant = 0
	r.mu.Unlock()
	for _, run := range runs {
		run.cancel()
	}
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

func protocolMode(mode string) string { return NormalizeChatMode(mode) }

// notionTimestamp matches the millisecond, offset-preserving timestamps emitted
// by the browser client (for example 2026-09-26T22:37:52.460+03:00).
func notionTimestamp(now time.Time) string {
	return now.Format("2006-01-02T15:04:05.000Z07:00")
}

func (r *Runtime) state(conversationID, spaceID, mode string) *conversation {
	r.sweep()
	mode = protocolMode(mode)
	convo, ok := r.convos[conversationID]
	if !ok || convo.SpaceID != spaceID || convo.Mode != mode {
		convo = &conversation{
			ThreadID: uid.New(), SpaceID: spaceID, Mode: mode,
			AgentThread: mode == ModeV3,
		}
		if r.threadLoad != nil {
			if saved := strings.TrimSpace(r.threadLoad(spaceID, conversationID, mode)); saved != "" {
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
	requestedMode := protocolMode(req.Mode)
	capturedMode := protocolMode(cfg.CaptureMode)
	if requestedMode != capturedMode {
		return fmt.Errorf(
			"для режима %s импортируйте Copy as cURL соответствующего запроса; активная browser-сессия относится к %s",
			requestedMode, capturedMode,
		)
	}
	if req.ConversationID == "" {
		req.ConversationID = uid.New()
	}

	// Agent Service V3 lives in agentthread.go and manages its own polling.
	if AgentMode(req.Mode) {
		return r.streamAgent(ctx, cfg, req, emit)
	}
	if AgentMode(cfg.CaptureMode) || len(cfg.Template) == 0 {
		return errors.New("режимам legacy/V2 нужен Copy as cURL запроса runInferenceTranscript; текущая сессия импортирована из Agent Service V3")
	}

	ctx, cancel := context.WithCancel(ctx)
	runID := uid.New()
	r.mu.Lock()
	previous, hadPrevious := r.cancels[req.ConversationID]
	r.cancels[req.ConversationID] = activeRun{id: runID, cancel: cancel}
	body, convo, edit, err := r.buildBody(cfg, req)
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
	if err != nil {
		return err
	}

	if edit != nil {
		if err := r.applyMessageEdit(ctx, convo, edit); err != nil {
			return err
		}
	} else if WorkflowV2Mode(req.Mode) && convo.Started {
		// Browser V2 persists context + updated-config + user before starting
		// the partial inference. This makes submittedUserStepId durable and
		// prevents a network interruption from losing the user's turn.
		if err := r.persistWorkflowV2Steps(ctx, cfg, convo, body); err != nil {
			return err
		}
	}

	inferenceCtx := ctx
	if WorkflowV2Mode(req.Mode) {
		// The browser uses /ai for thread creation and /chat?t=<thread> for
		// continuation. A cURL captured from the other phase carries the wrong
		// Referer, which Notion accepts at HTTP level but rejects inside the
		// patch stream as temporarily-unavailable.
		referer := strings.TrimRight(cfg.Origin, "/") + "/ai"
		if convo.Started {
			referer = strings.TrimRight(cfg.Origin, "/") + "/chat?t=" + convo.ThreadID
		}
		inferenceCtx = withHeaderOverrides(ctx, map[string]string{"referer": referer}, true)
	}
	resp, debug, err := r.client.Post(inferenceCtx, inferencePath, body)
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

	// Do not mark the thread as started yet. runInferenceTranscript may return
	// HTTP 200 and then emit an NDJSON error frame; persisting the mapping before
	// the stream succeeds makes the next retry target a thread that may not exist.
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

	var streamErr error
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
			if event.Type == "error" && streamErr == nil {
				message := event.Message
				if message == "" {
					message = event.Delta
				}
				streamErr = errors.New(message)
			}
		}
		if kind, _ := frame["type"].(string); kind == "error" {
			streamErr = errors.New(frameErrorMessage(frame))
		}
		if streamErr != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		r.client.finishDebug(debug, err)
		return err
	}
	if streamErr != nil {
		r.client.finishDebug(debug, streamErr)
		return streamErr
	}

	r.mu.Lock()
	convo.Started = true
	save, threadID, spaceID := r.threadSave, convo.ThreadID, convo.SpaceID
	r.mu.Unlock()
	if save != nil {
		if err := save(spaceID, req.ConversationID, protocolMode(req.Mode), threadID); err != nil {
			r.client.finishDebug(debug, err)
			return fmt.Errorf("не удалось сохранить привязку чата: %w", err)
		}
	}

	r.client.finishDebug(debug, nil)
	safeEmit(Event{Type: "done"})
	return nil
}

func frameErrorMessage(frame map[string]interface{}) string {
	var find func(interface{}, int) string
	find = func(value interface{}, depth int) string {
		if depth > 5 {
			return ""
		}
		switch typed := value.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if text != "" && !strings.EqualFold(text, "error") {
				return text
			}
		case map[string]interface{}:
			for _, key := range []string{"message", "error", "reason", "detail", "details", "data"} {
				if nested, ok := typed[key]; ok {
					if text := find(nested, depth+1); text != "" {
						return text
					}
				}
			}
		case []interface{}:
			for _, nested := range typed {
				if text := find(nested, depth+1); text != "" {
					return text
				}
			}
		}
		return ""
	}
	if message := find(frame, 0); message != "" {
		return message
	}
	return "Notion вернул ошибку в потоке"
}

func snapshotErrorMessage(snapshot interface{}) string {
	steps, _ := snapshot.([]interface{})
	for _, raw := range steps {
		step, _ := raw.(map[string]interface{})
		if kind, _ := step["type"].(string); kind == "error" {
			return frameErrorMessage(step)
		}
	}
	return ""
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
		if message := snapshotErrorMessage(data["s"]); message != "" {
			return []Event{{Type: "error", Delta: message, Message: message, Label: message}}
		}
		return acc.Reset(data["s"])
	case "patch-sync":
		// Раньше здесь шёл transcript-reset: UI выбрасывал весь ответ и
		// начинал проявлять его заново на каждой синхронизации. Аккумулятор
		// теперь сам сравнивает снимок с уже показанным и отдаёт только хвост.
		data, _ := frame["data"].(map[string]interface{})
		if message := snapshotErrorMessage(data["s"]); message != "" {
			return []Event{{Type: "error", Delta: message, Message: message, Label: message}}
		}
		return acc.Reset(data["s"])
	case "patch":
		return acc.Apply(frame["v"])
	case "record-map":
		if title := r.absorbRecordMap(convo, frame["recordMap"]); title != "" {
			return []Event{{Type: "thread-title", Title: title}}
		}
	case "error":
		message := frameErrorMessage(frame)
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

	convo := r.state(req.ConversationID, cfg.SpaceID, req.Mode)
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
	prompt := strings.TrimSpace(req.SystemPrompt)
	promptHasNativeField := false

	if workflow != nil {
		step := cloneMap(workflow)
		if id, _ := step["id"].(string); id == "" || !hasThread {
			step["id"] = uid.New()
		}
		if value, ok := step["value"].(map[string]interface{}); ok {
			if WorkflowV2Mode(req.Mode) && !hasThread {
				// A cURL copied from a continuation contains fields describing the
				// already-started thread. Replaying those fields while createThread is
				// true makes Notion return a patch-start snapshot whose only step is
				// temporarily-unavailable. Normalize to the fresh-chat contract seen
				// in the browser HAR.
				for _, key := range []string{
					"isThreadStartedByAdmin", "canAccessAllAgentThreads",
					"useContextualCoreDocsAutoLoad", "useDocPreviewsForCoreAutoLoad",
				} {
					delete(value, key)
				}
				if _, ok := value["availableConnectors"]; !ok {
					value["availableConnectors"] = []interface{}{}
				}
				if _, ok := value["updatePageStaleViewGuardEnabled"]; ok {
					value["updatePageStaleViewGuardEnabled"] = false
				}
				if strings.TrimSpace(req.Model) == "" {
					delete(value, "model")
					delete(value, "reasoningEffort")
					value["modelFromUser"] = false
				}
			}
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
			if WorkflowV2Mode(req.Mode) {
				// Beta V2 carries agent controls inside the workflow config rather
				// than in a separate Agent Service request.
				value["agentMemorySettings"] = map[string]interface{}{
					"useMemories":             req.UseMemories,
					"excludeChatFromMemories": req.ExcludeFromMemories,
				}
				value["enableSuggestedEditsTools"] = req.SuggestedEdits
				value["internetAccess"] = req.BrowserEnabled
				value["useWebSearch"] = req.BrowserEnabled
			}
			// V2's config schema is strict. The September 2026 browser contract
			// contains neither customInstructions nor systemPrompt, so never invent
			// those keys. Only update a native field when the imported capture
			// already proves that the selected protocol supports it; otherwise the
			// prompt is prepended to the user step below.
			if prompt != "" {
				if _, ok := value["customInstructions"]; ok {
					value["customInstructions"] = prompt
					promptHasNativeField = true
				}
				if _, ok := value["systemPrompt"]; ok {
					value["systemPrompt"] = prompt
					promptHasNativeField = true
				}
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
			value["currentDatetime"] = notionTimestamp(time.Now())
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

	userContent := latest.Content
	if prompt != "" && !promptHasNativeField {
		userContent = prompt
		if strings.TrimSpace(latest.Content) != "" {
			userContent += "\n\n" + latest.Content
		}
	}
	userStepValue := map[string]interface{}{}
	if userTemplate != nil {
		userStepValue = cloneMap(userTemplate)
	}
	userStepValue["id"] = notionUserID
	userStepValue["type"] = "user"
	if _, ok := userStepValue["userId"]; !ok && cfg.UserID != "" {
		userStepValue["userId"] = cfg.UserID
	}
	userStepValue["value"] = []interface{}{[]interface{}{userContent}}
	userStepValue["createdAt"] = notionTimestamp(time.Now())
	steps = append(steps, userStepValue)

	var edit *messageEdit
	if isEdit {
		edit = &messageEdit{UserStepID: notionUserID, Content: userContent}
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
	if WorkflowV2Mode(req.Mode) {
		// These fields are explicit in the Beta V2 browser contract. Do not
		// rely on an old imported template to happen to contain them.
		body["spaceId"] = cfg.SpaceID
		body["submittedUserStepId"] = notionUserID
		if _, ok := body["expectedFundingRoute"]; !ok {
			body["expectedFundingRoute"] = "ordinary"
		}
	}
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

// persistWorkflowV2Steps mirrors WorkflowActions.addStepsToExistingThreadAndRun
// from the Beta V2 browser HAR. Continuation turns are pre-saved before
// inference. In particular, uploaded attachment steps must be persisted before
// updated-config and user or V2 receives the file URL but cannot resolve the
// corresponding thread_message.
func (r *Runtime) persistWorkflowV2Steps(
	ctx context.Context, cfg *Config, convo *conversation, body map[string]interface{},
) error {
	transcript, _ := body["transcript"].([]interface{})
	contextStep := lastStepOfType(transcript, "context")
	updatedStep := lastStepOfType(transcript, "updated-config")
	userStep := lastStepOfType(transcript, "user")
	if contextStep == nil || updatedStep == nil || userStep == nil {
		return errors.New("V2 continuation transcript must contain context, updated-config and user steps")
	}

	// The persisted updated-config record carries availableConnectors even
	// though the compact partial transcript may omit its value.
	updatedStep = cloneMap(updatedStep)
	if _, ok := updatedStep["value"]; !ok {
		updatedStep["value"] = map[string]interface{}{"availableConnectors": []interface{}{}}
	}

	// The upload HAR persists attachment(s) in the same transaction and before
	// updated-config/user. Keeping context first preserves the normal V2
	// continuation contract while making uploaded files durable and visible to
	// runInferenceTranscript.
	persisted := []map[string]interface{}{contextStep}
	for _, raw := range transcript {
		step, _ := raw.(map[string]interface{})
		kind, _ := step["type"].(string)
		if kind == "attachment" || kind == "computer-file" {
			persisted = append(persisted, step)
		}
	}
	persisted = append(persisted, updatedStep, userStep)

	now := time.Now().UnixMilli()
	ids := make([]interface{}, 0, len(persisted))
	operations := make([]interface{}, 0, len(persisted)+1)
	for _, original := range persisted {
		step := cloneMap(original)
		id, _ := step["id"].(string)
		if strings.TrimSpace(id) == "" {
			return errors.New("V2 pre-save step has no id")
		}
		ids = append(ids, id)
		operations = append(operations, map[string]interface{}{
			"pointer": map[string]interface{}{
				"table": "thread_message", "id": id, "spaceId": convo.SpaceID,
			},
			"path": []interface{}{}, "command": "set",
			"args": map[string]interface{}{
				"id": id, "version": 1, "space_id": convo.SpaceID,
				"parent_id": convo.ThreadID, "parent_table": "thread",
				"created_by_id": cfg.UserID, "created_by_table": "notion_user",
				"created_time": now, "step": step,
			},
		})
	}
	operations = append(operations, map[string]interface{}{
		"pointer": r.threadPointer(convo), "path": []interface{}{"messages"},
		"command": "listAfterMulti", "args": map[string]interface{}{"ids": ids},
	})

	saveCtx := withHeaderOverrides(ctx, map[string]string{
		"accept":  "*/*",
		"referer": strings.TrimRight(cfg.Origin, "/") + "/chat?t=" + convo.ThreadID,
	}, true)
	_, err := r.client.PostJSON(saveCtx, transactionsFanoutPath, map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{
			map[string]interface{}{
				"id": uid.New(), "spaceId": convo.SpaceID,
				"debug": map[string]interface{}{
					"userAction":         "WorkflowActions.addStepsToExistingThreadAndRun",
					"clientCommitTimeMs": now,
				},
				"operations": operations,
			},
			map[string]interface{}{
				"id": uid.New(), "spaceId": convo.SpaceID,
				"debug": map[string]interface{}{
					"userAction":         "unifiedChatInputActions.updateThreadUpdatedTime",
					"clientCommitTimeMs": now,
				},
				"operations": []interface{}{map[string]interface{}{
					"pointer": r.threadPointer(convo), "path": []interface{}{}, "command": "update",
					"args": map[string]interface{}{
						"updated_by_id": cfg.UserID, "updated_by_table": "notion_user", "updated_time": now,
					},
				}},
			},
		},
	})
	return err
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
	run, running := r.cancels[conversationID]
	convo := r.convos[conversationID]
	r.mu.Unlock()

	if running {
		run.cancel()
	}
	if convo == nil {
		return nil
	}
	// В новом протоколе current_inference_id у треда нет: ход останавливается
	// отменой локального опроса плюс best-effort стоп-событием.
	if convo.AgentThread {
		return r.stopAgentThread(ctx, convo.SpaceID, convo.ThreadID)
	}
	_, err := r.client.PostJSON(ctx, transactionsFanoutPath, map[string]interface{}{
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

type workspaceModelPolicy struct {
	found             bool
	disabledModels    map[string]bool
	disabledProviders map[string]bool
}

func stringSet(value interface{}) map[string]bool {
	out := map[string]bool{}
	items, _ := value.([]interface{})
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out[strings.ToLower(strings.TrimSpace(text))] = true
		}
	}
	return out
}

// workspaceModelsPolicy читает фактическую политику текущего workspace.
// Новый personal agent и legacy workflow намеренно используют разные policy.
func (r *Runtime) workspaceModelsPolicy(ctx context.Context, cfg *Config, mode string) (workspaceModelPolicy, error) {
	result := workspaceModelPolicy{
		disabledModels: map[string]bool{}, disabledProviders: map[string]bool{},
	}
	raw, err := r.client.PostJSON(ctx, pathGetSpaces, map[string]interface{}{})
	if err != nil {
		return result, err
	}
	bucket := asMap(raw[cfg.UserID])
	if bucket == nil {
		return result, errors.New("getSpaces не вернул активный аккаунт")
	}
	for id, record := range asMap(bucket["space"]) {
		space := spaceRecord(record)
		if space == nil || (id != cfg.SpaceID && str(space, "id") != cfg.SpaceID) {
			continue
		}
		settings := asMap(space["settings"])
		policyName := "custom_agent_model_policy"
		if AgentMode(mode) {
			policyName = "personal_agent_model_policy"
		}
		policy := asMap(settings[policyName])
		if policy == nil {
			return result, nil
		}
		result.found = true
		result.disabledModels = stringSet(policy["disabledModels"])
		result.disabledProviders = stringSet(policy["disabledProviders"])
		return result, nil
	}
	return result, errors.New("getSpaces не вернул активный workspace")
}

func modelsAllowedByPolicy(catalog []Model, policy workspaceModelPolicy) []Model {
	out := make([]Model, 0, len(catalog))
	for _, model := range catalog {
		if policy.disabledModels[strings.ToLower(model.ID)] ||
			policy.disabledProviders[strings.ToLower(model.Provider)] {
			continue
		}
		out = append(out, model)
	}
	return out
}

// Models возвращает модели именно для выбранного протокола: workflow для
// legacy и agentService для нового personal-agent режима.
func (r *Runtime) Models(ctx context.Context, mode string) ([]Model, error) {
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
	runtimeName := "workflow"
	if AgentMode(mode) {
		runtimeName = "agentService"
	}
	for _, item := range raw {
		model, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if disabled, _ := model["isDisabled"].(bool); disabled {
			continue
		}
		if AgentMode(mode) {
			if restricted, _ := model["restrictedForPersonalAgent"].(bool); restricted {
				continue
			}
		}
		runtimeConfig, _ := model[runtimeName].(map[string]interface{})
		if runtimeConfig == nil {
			continue
		}
		if disabled, _ := runtimeConfig["isDisabled"].(bool); disabled {
			continue
		}
		if name, _ := runtimeConfig["finalModelName"].(string); name == "" {
			continue
		}
		id, _ := model["model"].(string)
		if id == "" {
			continue
		}
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
	// В agent-service getAvailableModels может вернуть либо глобальный каталог,
	// либо пустой models[] + modelSelectionRestricted. Источник истины о том,
	// что реально включено в конкретном workspace, — personal_agent_model_policy.
	// Поэтому новый режим всегда накладывает policy, даже на непустой ответ API.
	if AgentMode(mode) {
		policy, policyErr := r.workspaceModelsPolicy(ctx, cfg, mode)
		if policyErr != nil {
			return nil, fmt.Errorf("не удалось получить активные модели workspace: %w", policyErr)
		}
		if !policy.found {
			return nil, errors.New("Notion не вернул personal_agent_model_policy текущего workspace")
		}
		if len(out) > 0 {
			return modelsAllowedByPolicy(out, policy), nil
		}
		// При скрытом picker берём полный каталог кодовых имён из рабочего HAR,
		// но показываем только модели, не отключённые текущей policy.
		return modelsAllowedByPolicy(knownModels(), policy), nil
	}

	restricted, _ := payload["modelSelectionRestricted"].(bool)
	if WorkflowV2Mode(mode) {
		// V2 may hide models[] behind modelSelectionRestricted even though the
		// workflow accepts an explicit model code. Whether the endpoint returns a
		// picker list or not, custom_agent_model_policy is the workspace source
		// of truth and must be applied after an admin changes the checkboxes.
		policy, policyErr := r.workspaceModelsPolicy(ctx, cfg, mode)
		if policyErr == nil && policy.found {
			catalog := out
			if len(catalog) == 0 {
				catalog = knownModels()
			}
			return modelsAllowedByPolicy(catalog, policy), nil
		}
		if restricted {
			return []Model{}, nil
		}
		if policyErr != nil {
			return nil, policyErr
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if restricted {
		return []Model{}, nil
	}

	// Legacy fallback preserves the old custom_agent_model_policy behavior.
	policy, err := r.workspaceModelsPolicy(ctx, cfg, mode)
	if err != nil {
		return nil, err
	}
	if !policy.found {
		return nil, errors.New("Notion скрыл список моделей, а policy текущего workspace отсутствует")
	}
	return modelsAllowedByPolicy(knownModels(), policy), nil
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
