package notion

// Синхронизация чата с Notion.
//
// Раньше история жила только в локальной SQLite и писалась в конце ответа.
// Из-за этого: (1) при открытии чата, созданного в веб-версии или на другом
// устройстве, переписка выглядела пустой; (2) если поток обрывался, ответ
// терялся, хотя Notion его дописал. Теперь состояние чата читается прямо из
// записей Notion (thread + thread_message) через syncRecordValuesMain и
// проецируется теми же правилами, что и живой стрим.

import (
	"context"
	"strings"
	"time"
)

// ThreadPart — часть ответа в том же виде, в каком её рисует UI.
type ThreadPart struct {
	Kind   string                 `json:"kind"` // text | thought | tool
	Text   string                 `json:"text,omitempty"`
	ID     string                 `json:"id,omitempty"`
	Name   string                 `json:"name,omitempty"`
	Server string                 `json:"server,omitempty"`
	Args   map[string]interface{} `json:"args,omitempty"`
	Result interface{}            `json:"result,omitempty"`
	Done   bool                   `json:"done,omitempty"`
}

// ThreadTurn — одна реплика чата, восстановленная из Notion.
type ThreadTurn struct {
	ID        string       `json:"id"`
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	Parts     []ThreadPart `json:"parts"`
	CreatedAt int64        `json:"createdAt"`
}

// ThreadState — снимок чата на стороне Notion.
type ThreadState struct {
	ThreadID string       `json:"threadId"`
	Title    string       `json:"title"`
	// Running = Notion прямо сейчас генерирует ответ (current_inference_id).
	Running bool         `json:"running"`
	Found   bool         `json:"found"`
	Turns   []ThreadTurn `json:"turns"`
}

// IsRunning сообщает, идёт ли поток этого чата прямо в приложении.
func (r *Runtime) IsRunning(conversationID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[conversationID]
	return ok
}

// RunningConversations — все чаты, которые сейчас стримятся в приложении.
func (r *Runtime) RunningConversations() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.cancels))
	for id := range r.cancels {
		out = append(out, id)
	}
	return out
}

// RemoteThreadID переводит локальный чат в thread на стороне Notion.
// Чаты, пришедшие из списка Notion, уже имеют удалённый id.
func (r *Runtime) RemoteThreadID(spaceID, conversationID string) string {
	r.mu.Lock()
	convo, ok := r.convos[conversationID]
	load := r.threadLoad
	r.mu.Unlock()
	if ok && convo.Started && strings.TrimSpace(convo.ThreadID) != "" {
		return convo.ThreadID
	}
	if load != nil {
		if saved := strings.TrimSpace(load(spaceID, conversationID)); saved != "" {
			return saved
		}
	}
	return conversationID
}

// ThreadState читает транскрипт чата из Notion и собирает готовые реплики.
func (r *Runtime) ThreadState(ctx context.Context, conversationID string) (ThreadState, error) {
	cfg, err := r.client.require()
	if err != nil {
		return ThreadState{}, err
	}
	spaceID := cfg.SpaceID
	threadID := r.RemoteThreadID(spaceID, conversationID)
	state := ThreadState{ThreadID: threadID}
	if strings.TrimSpace(threadID) == "" {
		return state, nil
	}

	records, err := r.client.fetchRecords(ctx, spaceID, "thread", []string{threadID})
	if err != nil {
		return state, err
	}
	thread := records[threadID]
	if thread == nil {
		return state, nil
	}
	state.Found = true
	state.Running = inferenceRunning(thread)
	if data, ok := thread["data"].(map[string]interface{}); ok {
		if title, ok := data["title"].(string); ok {
			state.Title = strings.TrimSpace(title)
		}
	}
	if state.Title == "" {
		if title, ok := thread["title"].(string); ok {
			state.Title = strings.TrimSpace(title)
		}
	}

	var ids []string
	if list, ok := thread["messages"].([]interface{}); ok {
		for _, raw := range list {
			if id, ok := raw.(string); ok && strings.TrimSpace(id) != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return state, nil
	}

	messages, err := r.client.fetchRecords(ctx, spaceID, "thread_message", ids)
	if err != nil {
		return state, err
	}
	state.Turns = turnsFromMessages(ids, messages)
	return state, nil
}

// fetchRecords читает записи Notion пачками (пойнтеры + version -1).
func (c *Client) fetchRecords(
	ctx context.Context, spaceID, table string, ids []string,
) (map[string]map[string]interface{}, error) {
	out := map[string]map[string]interface{}{}
	const batch = 60
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		requests := make([]interface{}, 0, end-start)
		for _, id := range ids[start:end] {
			requests = append(requests, map[string]interface{}{
				"pointer": map[string]interface{}{"table": table, "id": id, "spaceId": spaceID},
				"version": -1,
			})
		}
		payload, err := c.PostJSON(ctx, syncRecordsPath, map[string]interface{}{
			"requests":     requests,
			"spacePointer": map[string]interface{}{"table": "space", "id": spaceID},
		})
		if err != nil {
			return nil, err
		}
		for id, raw := range digMap(payload, "recordMap", table) {
			if value := recordValue(raw); value != nil {
				out[id] = value
			}
		}
	}
	return out, nil
}

// inferenceRunning — та же пара полей, которую гасит кнопка «Стоп».
func inferenceRunning(thread map[string]interface{}) bool {
	id, _ := thread["current_inference_id"].(string)
	if strings.TrimSpace(id) == "" {
		return false
	}
	lease, ok := thread["current_inference_lease_expiration"].(float64)
	if !ok || lease <= 0 {
		return true
	}
	expires := int64(lease)
	if expires < 1e12 { // значение в секундах
		expires *= 1000
	}
	// Небольшой запас: Notion продлевает аренду не мгновенно.
	return time.Now().UnixMilli() < expires+30_000
}

// turnsFromMessages склеивает шаги thread_message в реплики UI.
// Служебные шаги (workflow/context/updated-config) в историю не попадают.
func turnsFromMessages(ids []string, records map[string]map[string]interface{}) []ThreadTurn {
	var turns []ThreadTurn
	var pending []interface{}
	pendingID := ""
	pendingAt := int64(0)

	flush := func() {
		if len(pending) == 0 {
			pending = nil
			pendingID = ""
			return
		}
		turn := ThreadTurn{ID: pendingID + "-a", Role: "assistant", CreatedAt: pendingAt}
		acc := &Accumulator{steps: pending}
		var text strings.Builder
		for _, item := range acc.project() {
			switch item.kind {
			case "text":
				if item.text == "" {
					continue
				}
				turn.Parts = append(turn.Parts, ThreadPart{Kind: "text", Text: item.text})
				text.WriteString(item.text)
			case "thinking":
				if item.text == "" {
					continue
				}
				turn.Parts = append(turn.Parts, ThreadPart{Kind: "thought", Text: item.text})
			case "tool_use":
				turn.Parts = append(turn.Parts, ThreadPart{
					Kind: "tool", ID: item.id, Name: item.name, Server: item.server,
					Args: item.args, Result: item.result, Done: item.done,
				})
			}
		}
		turn.Content = text.String()
		if len(turn.Parts) > 0 {
			turns = append(turns, turn)
		}
		pending = nil
		pendingID = ""
	}

	for _, id := range ids {
		record := records[id]
		if record == nil {
			continue
		}
		step, ok := record["step"].(map[string]interface{})
		if !ok {
			continue
		}
		createdAt := messageCreatedAt(record, step)
		switch kind, _ := step["type"].(string); kind {
		case "user":
			flush()
			content := userStepText(step)
			if strings.TrimSpace(content) == "" {
				continue
			}
			turns = append(turns, ThreadTurn{
				ID: id, Role: "user", Content: content, CreatedAt: createdAt,
				Parts: []ThreadPart{{Kind: "text", Text: content}},
			})
		case "workflow", "config", "context", "updated-config", "":
			// служебное
		default:
			if pendingID == "" {
				pendingID = id
				pendingAt = createdAt
			}
			pending = append(pending, step)
		}
	}
	flush()
	return turns
}

func messageCreatedAt(record, step map[string]interface{}) int64 {
	if value := int64Number(record["created_time"]); value > 0 {
		return value
	}
	if raw, ok := step["createdAt"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

// userStepText разбирает value вида [["текст"]], которое пишет веб-клиент.
func userStepText(step map[string]interface{}) string {
	switch value := step["value"].(type) {
	case string:
		return value
	case []interface{}:
		var sb strings.Builder
		for _, row := range value {
			switch cell := row.(type) {
			case string:
				sb.WriteString(cell)
			case []interface{}:
				for _, piece := range cell {
					if text, ok := piece.(string); ok {
						sb.WriteString(text)
					}
				}
			}
		}
		return sb.String()
	}
	return ""
}
