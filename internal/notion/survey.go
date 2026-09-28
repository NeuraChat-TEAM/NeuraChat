package notion

import (
	"context"
	"errors"
	"strings"

	"neura/internal/uid"
)

// sendEventPath — тот же эндпоинт, которым веб-клиент Notion отвечает на
// ask-survey: user.input_response уходит прямо в живой thread агента, поэтому
// ассистент продолжает начатый ход, а не начинает новый.
const sendEventPath = "/api/v3/sendEventToAgentThread"

// pendingInput — то, что нужно, чтобы ответ попал в нужный запрос агента.
type pendingInput struct {
	InteractionID string
	Sequence      int
	ToolName      string
}

// notePendingInput вылавливает interaction_id/request_sequence из любого кадра
// стрима. Notion уже несколько раз менял форму этих полей, поэтому ищем по
// именам ключей рекурсивно, а не по фиксированному пути.
func notePendingInput(frame interface{}, into *pendingInput, depth int) {
	if depth > 8 || into == nil {
		return
	}
	switch node := frame.(type) {
	case map[string]interface{}:
		for key, value := range node {
			switch key {
			case "interaction_id", "interactionId", "initialPendingInputId",
				"pendingInputId", "pending_input_id", "input_request_id", "inputRequestId":
				if text, ok := value.(string); ok && strings.HasPrefix(text, "event_") {
					into.InteractionID = text
				}
			case "request_sequence", "requestSequence", "sequence":
				if number, ok := value.(float64); ok {
					into.Sequence = int(number)
				}
			case "toolName", "name":
				if text, ok := value.(string); ok && isSurveyToolName(text) {
					into.ToolName = text
				}
			}
			notePendingInput(value, into, depth+1)
		}
	case []interface{}:
		for _, item := range node {
			notePendingInput(item, into, depth+1)
		}
	}
}

func isSurveyToolName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "survey") || strings.Contains(lower, "questionnaire")
}

// PendingSurvey сообщает, ждёт ли Notion структурированный ответ на опросник.
func (r *Runtime) PendingSurvey(conversationID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	convo, ok := r.convos[conversationID]
	return ok && convo.Pending.InteractionID != ""
}

// AnswerSurvey отправляет ответ опросника как user.input_response.
//
// content — это ровно то, что показывает карточка: {"<questionId>": "<optionId>"
// | ["<optionId>", ...], "other:<questionId>": "свободный текст"}.
// Возвращает false, если interaction_id поймать не удалось: тогда вызывающая
// сторона отправляет ответ обычным сообщением в чат.
func (r *Runtime) AnswerSurvey(
	ctx context.Context,
	conversationID, toolName string,
	content map[string]interface{},
	action string,
) (bool, error) {
	cfg, err := r.client.require()
	if err != nil {
		return false, err
	}

	r.mu.Lock()
	convo, ok := r.convos[conversationID]
	if !ok || convo.Pending.InteractionID == "" {
		r.mu.Unlock()
		return false, nil
	}
	// Ход уже завершён — продолжение пришло бы в мёртвый стрим, поэтому
	// отдаём ответ обратно в UI — он отправит его обычным сообщением.
	if _, running := r.cancels[conversationID]; !running {
		r.mu.Unlock()
		return false, nil
	}
	pending := convo.Pending
	threadID, spaceID := convo.ThreadID, convo.SpaceID
	// Один и тот же interaction_id принимается один раз.
	convo.Pending = pendingInput{}
	r.mu.Unlock()

	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	if threadID == "" {
		return false, errors.New("нет thread для ответа на опросник")
	}
	if strings.TrimSpace(toolName) == "" {
		toolName = pending.ToolName
	}
	if strings.TrimSpace(toolName) == "" {
		toolName = "ask-survey"
	}
	if strings.TrimSpace(action) == "" {
		action = "accept"
	}
	if content == nil {
		content = map[string]interface{}{}
	}

	body := map[string]interface{}{
		"spaceId":  spaceID,
		"threadId": threadID,
		"event": map[string]interface{}{
			"type":             "user.input_response",
			"interaction_id":   pending.InteractionID,
			"request_sequence": pending.Sequence,
			"input_responses": map[string]interface{}{
				toolName: map[string]interface{}{
					"action":  action,
					"content": content,
				},
			},
		},
		"clientEventId": uid.New(),
	}

	if _, err := r.client.PostJSON(ctx, sendEventPath, body); err != nil {
		// Ответ не принят — возвращаем pending обратно, чтобы UI мог
		// откатиться на обычное сообщение и не потерять выбор пользователя.
		r.mu.Lock()
		if convo, ok := r.convos[conversationID]; ok && convo.Pending.InteractionID == "" {
			convo.Pending = pending
		}
		r.mu.Unlock()
		return false, err
	}
	return true, nil
}
