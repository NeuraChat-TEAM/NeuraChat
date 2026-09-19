package application

import (
	"encoding/base64"
	"encoding/json"
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
		_ = a.cfg.SaveSettings(settings)
	}

	// The system prompt is always written first, ahead of any user text.
	prompt := strings.TrimSpace(settings.SystemPrompt)
	if prompt == "" && settings.AutoPrependMcp {
		prompt = a.DefaultSystemPrompt()
	}

	return a.runtime.Stream(a.ctx, notion.ChatRequest{
		ConversationID:  payload.ThreadID,
		Model:           model,
		ReasoningEffort: effort,
		SystemPrompt:    prompt,
		// «All sources I can access» по умолчанию выключено.
		SearchAllSources: settings.SearchAllSources,
		Messages:         payload.Messages,
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
	return a.runtime.UploadAttachment(
		a.ctx, payload.ThreadID, payload.FileName, payload.ContentType, data,
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
	return a.runtime.Models(a.ctx)
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
	// При запуске и после каждого переключения workspace фронтенд вызывает
	// ListThreads. Сначала подтягиваем актуальный список из Notion, затем отдаём
	// объединённую локальную базу. Сбой сети не прячет уже сохранённые чаты.
	if remote, err := a.client.ListInferenceThreads(a.ctx, spaceID); err == nil {
		threads := make([]store.Thread, 0, len(remote))
		for _, item := range remote {
			// runInferenceTranscript uses a local conversation id in the UI and a
			// different Notion thread id on the wire. Merge the remote row back
			// into its local conversation; otherwise the sidebar gets a duplicate
			// empty chat that has no messages in SQLite.
			id := item.ID
			if localID, lookupErr := a.db.ConversationForRemoteThread(spaceID, item.ID); lookupErr == nil && localID != "" {
				id = localID
				_ = a.db.DeleteThreadIfEmpty(item.ID)
			}
			threads = append(threads, store.Thread{
				ID: id, Title: item.Title, SpaceID: item.SpaceID,
				CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
			})
		}
		_ = a.db.UpsertThreads(threads)
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

func (a *App) RenameThread(id, title string) error { return a.db.RenameThread(id, title) }

func (a *App) DeleteThread(id string) error { return a.db.DeleteThread(id) }

func (a *App) LoadThread(id string) ([]store.Message, error) { return a.db.LoadMessages(id) }

// SaveMessage persists one turn, including its rendered timeline parts.
func (a *App) SaveMessage(message store.Message) error {
	if message.Parts != "" && !json.Valid([]byte(message.Parts)) {
		message.Parts = "[]"
	}
	// Чаты из старой базы закрепляются за тем воркспейсом, где их продолжили.
	_ = a.db.AdoptThread(message.ThreadID, a.activeSpace())
	return a.db.SaveMessage(message)
}

// ThreadSyncResult — состояние чата после сверки с Notion.
type ThreadSyncResult struct {
	// Running — ответ ещё генерируется (в приложении или в самом Notion).
	Running bool `json:"running"`
	// Streaming — поток идёт именно через это приложение, UI рисует его сам.
	Streaming bool            `json:"streaming"`
	Found     bool            `json:"found"`
	Title     string          `json:"title"`
	Messages  []store.Message `json:"messages"`
}

// SyncThread сверяет чат с Notion: подтягивает полную историю и говорит, идёт ли
// там генерация. Без этого чат, открытый после обрыва потока или созданный
// в веб-версии, выглядел пустым и «остановившимся».
func (a *App) SyncThread(threadID string) (ThreadSyncResult, error) {
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

	state, err := a.runtime.ThreadState(a.ctx, threadID)
	if err != nil {
		// Нет сети или сессии — отдаём то, что уже есть локально.
		if messages, dbErr := a.db.LoadMessages(threadID); dbErr == nil && len(messages) > 0 {
			result.Messages = messages
		}
		return result, err
	}

	result.Found = state.Found
	result.Running = state.Running
	result.Title = state.Title

	if len(state.Turns) == 0 {
		if messages, dbErr := a.db.LoadMessages(threadID); dbErr == nil && len(messages) > 0 {
			result.Messages = messages
		}
		return result, nil
	}

	spaceID := a.activeSpace()
	_ = a.db.EnsureThread(threadID, state.Title, spaceID)
	_ = a.db.AdoptThread(threadID, spaceID)

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
	if err := a.db.ReplaceMessages(threadID, messages); err != nil {
		return result, err
	}
	if state.Title != "" {
		_ = a.db.RenameThread(threadID, state.Title)
	}
	result.Messages = messages
	return result, nil
}

// RunningThreads — чаты, стрим которых живёт в приложении прямо сейчас.
func (a *App) RunningThreads() []string { return a.runtime.RunningConversations() }

func (a *App) TrimThreadFrom(threadID, messageID string) error {
	return a.db.DeleteMessagesFrom(threadID, messageID)
}
