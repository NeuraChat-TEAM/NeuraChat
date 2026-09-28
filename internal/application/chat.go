package application

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"neura/internal/notion"
	"neura/internal/store"
)

// SendPayload is what the composer sends.
type SendPayload struct {
	ThreadID        string           `json:"threadId"`
	Messages        []notion.Message `json:"messages"`
	Model           string           `json:"model"`
	ReasoningEffort string           `json:"reasoningEffort"`
}

// SendMessage streams an answer. Text/tool events arrive on the "chat:event"
// Wails event; this call returns once the stream completes.
func (a *App) SendMessage(payload SendPayload) error {
	// С этой секунды чат занят: сверка с Notion не имеет права заменять
	// историю, пока наше сообщение ещё не отразилось в записях.
	release := a.runtime.MarkBusy(payload.ThreadID)
	defer release()

	settings := a.cfg.LoadSettings()

	model := payload.Model
	if model == "" {
		model = settings.Model
	}
	effort := payload.ReasoningEffort
	if effort == "" {
		effort = settings.ReasoningEffort
	}

	// Remember the picker choice so the next app launch starts there.
	if payload.Model != "" && payload.Model != settings.Model {
		settings.Model = payload.Model
		settings.ReasoningEffort = effort
		if err := a.cfg.SaveSettings(settings); err != nil {
			return err
		}
	}

	// The system prompt is always written first, ahead of any user text.
	prompt := strings.TrimSpace(settings.SystemPrompt)
	if prompt == "" && settings.AutoPrependMcp {
		prompt = a.DefaultSystemPrompt()
	}

	mode := strings.TrimSpace(settings.ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}

	return a.runtime.Stream(a.ctx, notion.ChatRequest{
		ConversationID:  payload.ThreadID,
		Model:           model,
		ReasoningEffort: effort,
		SystemPrompt:    prompt,
		// «All sources I can access» по умолчанию выключено.
		SearchAllSources: settings.SearchAllSources,
		Messages:         payload.Messages,
		// Режим общения и опции нового протокола берём из настроек.
		Mode:                mode,
		BrowserEnabled:      settings.BrowserEnabled,
		SkipApprovals:       settings.SkipApprovals,
		UseMemories:         settings.UseMemories,
		ExcludeFromMemories: settings.ExcludeChatFromMemories,
		SuggestedEdits:      settings.SuggestedEdits,
	}, func(event notion.Event) {
		// События нескольких одновременных чатов идут по одному Wails-каналу.
		// ThreadID позволяет фронтенду обновить нужный чат, даже если пользователь
		// уже открыл другой и отправил там новый запрос.
		event.ThreadID = payload.ThreadID
		a.emit(event)
	})
}

// UploadPayload — один файл из композера: содержимое идёт base64,
// иначе бинарные байты не проходят через JSON-мост Wails.
type UploadPayload struct {
	ThreadID    string `json:"threadId"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	DataBase64  string `json:"dataBase64"`
}

// UploadAttachment загружает файл в Notion и возвращает готовое вложение
// для следующего сообщения.
func (a *App) UploadAttachment(payload UploadPayload) (notion.UploadResult, error) {
	var empty notion.UploadResult
	// Фронтенд может прислать data-URL — берём только хвост после запятой.
	raw := payload.DataBase64
	if index := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && index > 0 {
		raw = raw[index+1:]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return empty, err
	}
	// Загрузка файла идёт до sendMessage — тоже окно, в котором старый
	// снапшот может стереть уже показанное сообщение.
	release := a.runtime.MarkBusy(payload.ThreadID)
	defer release()
	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	return a.runtime.UploadAttachment(
		a.ctx, payload.ThreadID, payload.FileName, payload.ContentType, mode, data,
	)
}

// FetchAttachment достаёт содержимое файла из шага computer-file,
// чтобы показать его в правой панели-браузере или скачать.
// conversationID нужен, чтобы спрашивать ссылку в thread'е ТОГО чата,
// где файл родился: чужой thread даёт 404 и затем 403 на скачивании.
func (a *App) FetchAttachment(conversationID, fileURL, fileName string) (notion.FileContent, error) {
	return a.runtime.FetchAttachment(a.ctx, conversationID, fileURL, fileName)
}

// SendSurveyAnswer отвечает на ask-survey структурным событием user.input_response.
// false означает «агент больше не ждёт ввод» — UI отправляет ответ обычным
// сообщением в чат.
func (a *App) SendSurveyAnswer(
	conversationID, toolName string,
	content map[string]interface{},
) (bool, error) {
	return a.runtime.AnswerSurvey(a.ctx, conversationID, toolName, content, "accept")
}

func (a *App) StopInference(threadID string) error {
	return a.runtime.Stop(a.ctx, threadID)
}

func (a *App) ListModels() ([]notion.Model, error) {
	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	return a.runtime.Models(a.ctx, mode)
}

// Workspace model availability is separate from the chat picker. The admin
// surface shows the full catalog and persists one policy per protocol family.
func (a *App) WorkspaceModelsPolicy(mode string) (notion.WorkspaceModelPolicy, error) {
	if strings.TrimSpace(mode) == "" {
		mode = a.cfg.LoadSettings().ChatMode
	}
	return a.runtime.WorkspaceModelsPolicy(a.ctx, mode)
}

func (a *App) UpdateWorkspaceModelsPolicy(mode string, activeModelIDs []string) (notion.WorkspaceModelPolicy, error) {
	if strings.TrimSpace(mode) == "" {
		mode = a.cfg.LoadSettings().ChatMode
	}
	return a.runtime.UpdateWorkspaceModelsPolicy(a.ctx, mode, activeModelIDs)
}

// ---------------------------------------------------------------- threads

// activeSpace — воркспейс, к которому относятся чаты: берём из живой сессии,
// иначе из сохранённого выбора.
func (a *App) activeSpace() string {
	if cfg := a.client.Config(); cfg != nil && strings.TrimSpace(cfg.SpaceID) != "" {
		return cfg.SpaceID
	}
	return a.cfg.LoadSettings().ActiveSpaceID
}

func (a *App) ListThreads() ([]store.Thread, error) {
	spaceID := a.activeSpace()
	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	// При запуске и после каждого переключения workspace фронтенд вызывает
	// ListThreads. Сначала подтягиваем актуальный список из Notion, затем отдаём
	// объединённую локальную базу. Сбой сети не прячет уже сохранённые чаты.
	if remote, err := a.client.ListInferenceThreads(a.ctx, spaceID); err == nil {
		threads := make([]store.Thread, 0, len(remote))
		for _, item := range remote {
			hidden, hiddenErr := a.db.ThreadHidden(spaceID, mode, item.ID)
			if hiddenErr != nil {
				return nil, hiddenErr
			}
			if hidden {
				continue
			}
			// runInferenceTranscript uses a local conversation id in the UI and a
			// different Notion thread id on the wire. Merge the remote row back
			// into its local conversation; otherwise the sidebar gets a duplicate.
			id := item.ID
			if localID, lookupErr := a.db.ConversationForRemoteThread(spaceID, item.ID, mode); lookupErr == nil && localID != "" {
				id = localID
				if err := a.db.DeleteThreadIfEmpty(item.ID); err != nil {
					return nil, err
				}
			}
			title := item.Title
			if override, overrideErr := a.db.ThreadTitleOverride(spaceID, mode, item.ID); overrideErr != nil {
				return nil, overrideErr
			} else if strings.TrimSpace(override) != "" {
				title = override
			}
			threads = append(threads, store.Thread{
				ID: id, Title: title, SpaceID: item.SpaceID,
				CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
			})
		}
		if err := a.db.UpsertThreads(threads); err != nil {
			return nil, err
		}
	}
	return a.db.ListThreads(spaceID)
}

// SearchThreads — поиск по названиям и тексту сообщений текущего воркспейса.
func (a *App) SearchThreads(query string) ([]store.Thread, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return a.ListThreads()
	}
	return a.db.SearchThreads(a.activeSpace(), query, 30)
}

func (a *App) CreateThread(title string) (store.Thread, error) {
	if strings.TrimSpace(title) == "" {
		title = "Новый чат"
	}
	return a.db.CreateThread(title, a.activeSpace())
}

func (a *App) RenameThread(id, title string) error {
	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	spaceID := a.activeSpace()
	remoteID := a.runtime.RemoteThreadID(spaceID, id, mode)
	if err := a.db.SetThreadTitleOverride(spaceID, mode, remoteID, title); err != nil {
		return err
	}
	return a.db.RenameThread(id, title)
}

func (a *App) DeleteThread(id string) error {
	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	spaceID := a.activeSpace()
	remoteID := a.runtime.RemoteThreadID(spaceID, id, mode)
	if err := a.db.SetThreadHidden(spaceID, mode, remoteID, true); err != nil {
		return err
	}
	return a.db.DeleteThread(id)
}

func (a *App) LoadThread(id string) ([]store.Message, error) {
	if err := a.db.AdoptThread(id, a.activeSpace()); err != nil {
		return nil, err
	}
	return a.db.LoadMessages(id)
}

// SaveMessage persists one turn, including its rendered timeline parts.
func (a *App) SaveMessage(message store.Message) error {
	if message.Parts != "" && !json.Valid([]byte(message.Parts)) {
		message.Parts = "[]"
	}
	// Чаты из старой базы закрепляются за тем воркспейсом, где их продолжили.
	if err := a.db.AdoptThread(message.ThreadID, a.activeSpace()); err != nil {
		return err
	}
	return a.db.SaveMessage(message)
}

// ThreadSyncResult — состояние чата после сверки с Notion.
type ThreadSyncResult struct {
	// Running — ответ ещё генерируется (в приложении или в самом Notion).
	Running bool `json:"running"`
	// Streaming — поток идёт именно через это приложение, UI рисует его сам.
	Streaming  bool            `json:"streaming"`
	Found      bool            `json:"found"`
	HasEarlier bool            `json:"hasEarlier"`
	Title      string          `json:"title"`
	Messages   []store.Message `json:"messages"`
}

// SyncThread сверяет чат с Notion: подтягивает полную историю и говорит, идёт ли
// там генерация. Без этого чат, открытый после обрыва потока или созданный
// в веб-версии, выглядел пустым и «остановившимся».
func (a *App) SyncThread(threadID string) (ThreadSyncResult, error) {
	return a.syncThread(threadID, false)
}

func (a *App) LoadEarlierThread(threadID string) (ThreadSyncResult, error) {
	return a.syncThread(threadID, true)
}

func (a *App) syncThread(threadID string, loadAll bool) (ThreadSyncResult, error) {
	result := ThreadSyncResult{Messages: []store.Message{}}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return result, nil
	}
	if a.runtime.IsRunning(threadID) {
		// Свой живой стрим трогать нельзя: UI сам рисует дельты.
		result.Running, result.Streaming, result.Found = true, true, true
		messages, err := a.db.LoadMessages(threadID)
		if len(messages) > 0 {
			result.Messages = messages
		}
		return result, err
	}

	mode := strings.TrimSpace(a.cfg.LoadSettings().ChatMode)
	if mode == "" {
		mode = notion.ModeV2
	}
	var state notion.ThreadState
	var err error
	if loadAll {
		state, err = a.runtime.ThreadStateAll(a.ctx, threadID, mode)
	} else {
		state, err = a.runtime.ThreadState(a.ctx, threadID, mode)
	}
	if err != nil {
		// Нет сети или сессии — отдаём то, что уже есть локально.
		if messages, dbErr := a.db.LoadMessages(threadID); dbErr == nil && len(messages) > 0 {
			result.Messages = messages
		}
		return result, err
	}

	if state.Stale {
		// Notion отдал запись старше уже известной версии: в таком
		// снимке ещё нет свежего user-шага, и замена съедает сообщение.
		result.Found, result.Running = state.Found, state.Running
		if messages, dbErr := a.db.LoadMessages(threadID); dbErr == nil && len(messages) > 0 {
			result.Messages = messages
		}
		return result, nil
	}

	result.Found = state.Found
	result.Running = state.Running
	result.HasEarlier = state.HasEarlier
	result.Title = state.Title
	remoteID := a.runtime.RemoteThreadID(a.activeSpace(), threadID, mode)
	if override, overrideErr := a.db.ThreadTitleOverride(a.activeSpace(), mode, remoteID); overrideErr != nil {
		return result, overrideErr
	} else if strings.TrimSpace(override) != "" {
		result.Title = override
		state.Title = override
	}

	if len(state.Turns) == 0 {
		if messages, dbErr := a.db.LoadMessages(threadID); dbErr == nil && len(messages) > 0 {
			result.Messages = messages
		}
		return result, nil
	}

	spaceID := a.activeSpace()
	if err := a.db.EnsureThread(threadID, state.Title, spaceID); err != nil {
		return result, err
	}
	if err := a.db.AdoptThread(threadID, spaceID); err != nil {
		return result, err
	}

	// Порядок реплик берём из Notion, а метки времени делаем строго
	// возрастающими: история читается именно по created_at.
	messages := make([]store.Message, 0, len(state.Turns))
	previous := int64(0)
	for index, turn := range state.Turns {
		createdAt := turn.CreatedAt
		if createdAt <= previous {
			createdAt = previous + 1
		}
		if createdAt == 0 {
			createdAt = time.Now().UnixMilli() - int64(len(state.Turns)-index)
		}
		previous = createdAt
		parts, marshalErr := json.Marshal(turn.Parts)
		if marshalErr != nil {
			parts = []byte("[]")
		}
		messages = append(messages, store.Message{
			ID: turn.ID, ThreadID: threadID, Role: turn.Role,
			Content: turn.Content, Parts: string(parts), CreatedAt: createdAt,
		})
	}
	// Снапшот Notion отстаёт на 1–3 секунды, поэтому замена истории не
	// должна быть разрушающей: свежие локальные реплики сохраняем.
	if local, dbErr := a.db.LoadMessages(threadID); dbErr == nil {
		messages = mergeLocalTail(messages, local)
	}
	if err := a.db.ReplaceMessages(threadID, messages); err != nil {
		return result, err
	}
	if state.Title != "" {
		if err := a.db.RenameThread(threadID, state.Title); err != nil {
			return result, err
		}
	}
	result.Messages = messages
	return result, nil
}

// mergeLocalTail дописывает к снапшоту Notion те локальные сообщения, которых
// в нём ещё нет: они новее любой удалённой реплики и созданы недавно.
// Сопоставляем по паре role+текст: локальные id и id шагов Notion разные.
func mergeLocalTail(remote, local []store.Message) []store.Message {
	if len(local) == 0 {
		return remote
	}
	key := func(message store.Message) string {
		return message.Role + "\n" + strings.TrimSpace(message.Content)
	}
	seen := map[string]int{}
	newest := int64(0)
	for _, message := range remote {
		seen[key(message)]++
		if message.CreatedAt > newest {
			newest = message.CreatedAt
		}
	}
	// Старые локальные остатки не воскрешаем: удалённая история — истина.
	fresh := time.Now().UnixMilli() - 10*60*1000
	out := make([]store.Message, 0, len(remote)+2)
	out = append(out, remote...)
	for _, message := range local {
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		if seen[key(message)] > 0 {
			seen[key(message)]--
			continue
		}
		if message.CreatedAt <= newest || message.CreatedAt < fresh {
			continue
		}
		out = append(out, message)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// RunningThreads — чаты, стрим которых живёт в приложении прямо сейчас.
func (a *App) RunningThreads() []string { return a.runtime.RunningConversations() }

func (a *App) TrimThreadFrom(threadID, messageID string) error {
	return a.db.DeleteMessagesFrom(threadID, messageID)
}
