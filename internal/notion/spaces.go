package notion

import (
	"context"
	"errors"
	"sort"
	"strings"

	"neura/internal/uid"
)

// Воркспейсы и аккаунты для переключателя в сайдбаре.
//
// getSpaces отдаёт record map сразу по всем аккаунтам, залогиненным в одной
// cookie-сессии, поэтому одного запроса достаточно и для списка аккаунтов,
// и для их воркспейсов.
const (
	pathGetSpaces   = "/api/v3/getSpaces"
	pathCreateSpace = "/api/v3/createSpace"
	pathCanCreate   = "/api/v3/validateUserCanCreateWorkspace"
)

// Space — один воркспейс в списке.
type Space struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	PlanType    string `json:"planType"`
	SpaceViewID string `json:"spaceViewId"`
	IsGuest     bool   `json:"isGuest"`
	Active      bool   `json:"active"`
}

// Account — аккаунт Notion из текущей сессии вместе со своими воркспейсами.
type Account struct {
	UserID string  `json:"userId"`
	Name   string  `json:"name"`
	Email  string  `json:"email"`
	Avatar string  `json:"avatar"`
	Active bool    `json:"active"`
	Spaces []Space `json:"spaces"`
}

func asMap(value interface{}) map[string]interface{} {
	out, _ := value.(map[string]interface{})
	return out
}

// spaceRecord разворачивает обёртки Notion: {value:{value:{...}}} и {value:{...}}.
// Отдельная функция от recordValue в inference.go: там требуется строго два уровня.
func spaceRecord(record interface{}) map[string]interface{} {
	node := asMap(record)
	if node == nil {
		return nil
	}
	if inner := asMap(node["value"]); inner != nil {
		if deeper := asMap(inner["value"]); deeper != nil {
			return deeper
		}
		return inner
	}
	return node
}

func str(node map[string]interface{}, key string) string {
	if node == nil {
		return ""
	}
	out, _ := node[key].(string)
	return out
}

// Accounts возвращает все аккаунты сессии с их воркспейсами.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	raw, err := c.PostJSON(ctx, pathGetSpaces, map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	out := []Account{}
	for userID, payload := range raw {
		bucket := asMap(payload)
		if bucket == nil {
			continue
		}
		account := Account{UserID: userID, Active: userID == cfg.UserID, Spaces: []Space{}}

		// Профиль аккаунта: ищем запись notion_user с этим id.
		for id, record := range asMap(bucket["notion_user"]) {
			value := spaceRecord(record)
			if value == nil {
				continue
			}
			if id != userID && str(value, "id") != userID {
				continue
			}
			name := strings.TrimSpace(str(value, "name"))
			if name == "" {
				name = strings.TrimSpace(str(value, "given_name") + " " + str(value, "family_name"))
			}
			account.Name = strings.TrimSpace(name)
			account.Email = str(value, "email")
			account.Avatar = str(value, "profile_photo")
		}

		// space_view хранит связь аккаунт↔воркспейс (и нужен для MCP-модулей).
		views := map[string]string{}
		for id, record := range asMap(bucket["space_view"]) {
			value := spaceRecord(record)
			if value == nil {
				continue
			}
			viewID := str(value, "id")
			if viewID == "" {
				viewID = id
			}
			if spaceID := str(value, "space_id"); spaceID != "" {
				views[spaceID] = viewID
			}
		}

		for id, record := range asMap(bucket["space"]) {
			value := spaceRecord(record)
			if value == nil {
				continue
			}
			spaceID := str(value, "id")
			if spaceID == "" {
				spaceID = id
			}
			space := Space{
				ID:          spaceID,
				Name:        str(value, "name"),
				Icon:        str(value, "icon"),
				PlanType:    str(value, "plan_type"),
				SpaceViewID: views[spaceID],
				Active:      spaceID == cfg.SpaceID && userID == cfg.UserID,
			}
			// Гость: воркспейс виден, но своего space_view у аккаунта нет.
			space.IsGuest = space.SpaceViewID == ""
			if space.Name == "" {
				space.Name = "Без названия"
			}
			account.Spaces = append(account.Spaces, space)
		}

		sort.Slice(account.Spaces, func(i, j int) bool {
			if account.Spaces[i].IsGuest != account.Spaces[j].IsGuest {
				return !account.Spaces[i].IsGuest
			}
			return strings.ToLower(account.Spaces[i].Name) < strings.ToLower(account.Spaces[j].Name)
		})
		out = append(out, account)
	}

	// Активный аккаунт первым, остальные по имени.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return strings.ToLower(out[i].Email) < strings.ToLower(out[j].Email)
	})
	return out, nil
}

// CanCreateSpace проверяет, разрешено ли аккаунту создавать воркспейсы.
func (c *Client) CanCreateSpace(ctx context.Context) (bool, error) {
	raw, err := c.PostJSON(ctx, pathCanCreate, map[string]interface{}{})
	if err != nil {
		return false, err
	}
	allowed, _ := raw["canUserCreateSpace"].(bool)
	return allowed, nil
}

// CreateSpace создаёт воркспейс тем же запросом, что и веб-клиент из
// переключателя в сайдбаре, и возвращает его id + id space_view.
func (c *Client) CreateSpace(ctx context.Context, name string) (Space, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Space{}, errors.New("укажите название воркспейса")
	}
	raw, err := c.PostJSON(ctx, pathCreateSpace, map[string]interface{}{
		"name":            name,
		"planType":        "team",
		"planSelection":   "team",
		"initialPersona":  "unfilled",
		"deviceId":        uid.New(),
		"deviceType":      "web-desktop",
		"source":          "sidebar_switcher",
		"createSpaceView": true,
	})
	if err != nil {
		return Space{}, err
	}
	spaceID, _ := raw["spaceId"].(string)
	if spaceID == "" {
		return Space{}, errors.New("Notion не вернул id нового воркспейса")
	}

	space := Space{ID: spaceID, Name: name, PlanType: "team"}
	recordMap := asMap(raw["recordMap"])
	for _, record := range asMap(recordMap["space_view"]) {
		value := spaceRecord(record)
		if str(value, "space_id") == spaceID {
			space.SpaceViewID = str(value, "id")
		}
	}
	for _, record := range asMap(recordMap["space"]) {
		value := spaceRecord(record)
		if str(value, "id") == spaceID {
			if actual := str(value, "name"); actual != "" {
				space.Name = actual
			}
			space.Icon = str(value, "icon")
		}
	}
	return space, nil
}

// UseWorkspace переключает активный аккаунт/воркспейс: правит заголовки и
// шаблон запроса, чтобы следующий чат ушёл уже в выбранное пространство.
func (c *Client) UseWorkspace(userID, spaceID, spaceViewID, spaceName string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if spaceID == "" {
		return errors.New("не указан воркспейс")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if userID != "" {
		cfg.UserID = userID
		cfg.Headers["x-notion-active-user-header"] = userID
	}
	cfg.SpaceID = spaceID
	cfg.Headers["x-notion-space-id"] = spaceID
	// x-notion-cell-hint привязан к shard'у КОНКРЕТНОГО воркспейса (в HAR
	// видно пару prod-space-usw2-0003 + source=space-id-mapping). Если оставить
	// хинт от старого воркспейса, Notion обрабатывает запрос в чужой cell
	// и отвечает 403 (в том числе «Custom MCP servers are disabled»). Без хинта
	// Notion сам определит cell по spaceId.
	delete(cfg.Headers, "x-notion-cell-hint")
	delete(cfg.Headers, "x-notion-cell-hint-source")
	// Всегда заменяем spaceViewID. Иначе при переходе в guest/new workspace
	// без view оставался id предыдущего пространства и MCP-транзакция уходила
	// в чужой space_view.
	cfg.SpaceViewID = spaceViewID

	cfg.Template["spaceId"] = spaceID
	cfg.Template["threadParentPointer"] = map[string]interface{}{
		"table": "space", "id": spaceID, "spaceId": spaceID,
	}
	// Нельзя переиспользовать threadId из другого пространства.
	delete(cfg.Template, "threadId")

	transcript, _ := cfg.Template["transcript"].([]interface{})
	for _, raw := range transcript {
		step := asMap(raw)
		if step == nil {
			continue
		}
		if kind, _ := step["type"].(string); kind != "context" {
			continue
		}
		value := asMap(step["value"])
		if value == nil {
			continue
		}
		value["spaceId"] = spaceID
		if spaceViewID != "" {
			value["spaceViewId"] = spaceViewID
		} else {
			delete(value, "spaceViewId")
		}
		if spaceName != "" {
			value["spaceName"] = spaceName
		}
		if userID != "" {
			value["userId"] = userID
		}
	}
	return nil
}
