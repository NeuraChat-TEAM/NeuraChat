package notion

import (
	"context"
	"strings"
)

const inferenceThreadsPath = "/api/v3/getInferenceTranscriptsForUser"

// InferenceThread — чат Notion AI, который показывается в левом сайдбаре.
type InferenceThread struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// ListInferenceThreads получает список чатов прямо из выбранного workspace.
// Форма запроса снята с HAR веб-клиента Notion. Без limit клиент возвращает
// полный список и сам выставляет hasMore/recordMap.
func (c *Client) ListInferenceThreads(ctx context.Context, spaceID string) ([]InferenceThread, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	spaceID = strings.TrimSpace(spaceID)
	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	out, err := c.PostJSON(ctx, inferenceThreadsPath, map[string]interface{}{
		"threadParentPointer": map[string]interface{}{
			"table": "space", "id": spaceID, "spaceId": spaceID,
		},
		"includeWorkflowThreads": true,
		"includeWriterChats":     false,
	})
	if err != nil {
		return nil, err
	}

	rows, _ := out["transcripts"].([]interface{})
	result := make([]InferenceThread, 0, len(rows))
	seen := map[string]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]interface{})
		if row == nil {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" || seen[id] {
			continue
		}
		title, _ := row["title"].(string)
		if strings.TrimSpace(title) == "" {
			title = "Новый чат"
		}
		created := int64Number(row["created_at"])
		updated := int64Number(row["updated_at"])
		if updated == 0 {
			updated = created
		}
		result = append(result, InferenceThread{
			ID: id, Title: title, SpaceID: spaceID, CreatedAt: created, UpdatedAt: updated,
		})
		seen[id] = true
	}
	return result, nil
}

func int64Number(value interface{}) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	}
	return 0
}
