package notion

// Участники воркспейса: список, приглашение и исключение.
//
// Все запросы повторяют цепочку веб-клиента из workspace-members-invite.har:
//   1. getVisibleUsers            — кто уже состоит в воркспейсе;
//   2. findUser                   — поиск аккаунта по почте перед приглашением;
//   3. inviteGuestsToSpace        — создание приглашения (action=create_invites);
//   4. addMembersToSpace          — добавление найденного пользователя участником;
//   5. saveTransactionsFanout     — переименование воркспейса и смена иконки.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"neura/internal/uid"
)

const (
	pathVisibleUsers  = "/api/v3/getVisibleUsers"
	pathFindUser      = "/api/v3/findUser"
	pathInviteGuests  = "/api/v3/inviteGuestsToSpace"
	pathAddMembers    = "/api/v3/addMembersToSpace"
	pathRemoveMembers = "/api/v3/removeUsersFromSpace"
	pathUserCounts    = "/api/v3/getSpaceUserCountsByType"
)

// Member — участник воркспейса для списка в настройках.
type Member struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
	Role   string `json:"role"`
	IsSelf bool   `json:"isSelf"`
	IsOwner bool  `json:"isOwner"`
}

// Members возвращает участников указанного воркспейса.
// spaceID пустой — берём активный из конфигурации.
func (c *Client) Members(ctx context.Context, spaceID string) ([]Member, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
	}
	if spaceID == "" {
		return nil, errors.New("не указан воркспейс")
	}
	raw, err := c.PostJSON(ctx, pathVisibleUsers, map[string]interface{}{
		"spaceId":                spaceID,
		"supportsEdgeCache":      true,
		"earlyReturnForEdgeCache": true,
	})
	if err != nil {
		return nil, err
	}

	// Роли лежат отдельно от профилей: permissions/роль по user_id.
	roles := map[string]string{}
	for _, key := range []string{"permissions", "rolesByUserId", "userRoles"} {
		switch node := raw[key].(type) {
		case map[string]interface{}:
			for id, value := range node {
				if text, ok := value.(string); ok {
					roles[id] = text
					continue
				}
				if item := asMap(value); item != nil {
					if text := str(item, "role"); text != "" {
						roles[id] = text
					}
				}
			}
		case []interface{}:
			for _, value := range node {
				item := asMap(value)
				if item == nil {
					continue
				}
				id := str(item, "userId")
				if id == "" {
					id = str(item, "user_id")
				}
				if id != "" {
					roles[id] = str(item, "role")
				}
			}
		}
	}

	seen := map[string]bool{}
	out := []Member{}
	collect := func(value map[string]interface{}, id string) {
		if value == nil {
			return
		}
		userID := str(value, "id")
		if userID == "" {
			userID = id
		}
		if userID == "" || seen[userID] {
			return
		}
		seen[userID] = true
		name := strings.TrimSpace(str(value, "name"))
		if name == "" {
			name = strings.TrimSpace(str(value, "given_name") + " " + str(value, "family_name"))
		}
		email := str(value, "email")
		if name == "" {
			name = email
		}
		role := roles[userID]
		out = append(out, Member{
			UserID:  userID,
			Name:    name,
			Email:   email,
			Avatar:  str(value, "profile_photo"),
			Role:    role,
			IsSelf:  userID == cfg.UserID,
			IsOwner: strings.Contains(strings.ToLower(role), "owner") || strings.Contains(strings.ToLower(role), "admin"),
		})
	}

	// Ответ бывает и списком, и record map — поддерживаем оба варианта.
	if list, ok := raw["users"].([]interface{}); ok {
		for _, item := range list {
			node := asMap(item)
			if inner := asMap(node["value"]); inner != nil {
				node = inner
			}
			collect(node, "")
		}
	}
	if recordMap := asMap(raw["recordMap"]); recordMap != nil {
		for id, record := range asMap(recordMap["notion_user"]) {
			collect(spaceRecord(record), id)
		}
	}
	for id, record := range asMap(raw["notion_user"]) {
		collect(spaceRecord(record), id)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].IsSelf != out[j].IsSelf {
			return out[i].IsSelf
		}
		if out[i].IsOwner != out[j].IsOwner {
			return out[i].IsOwner
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// FindUser ищет аккаунт Notion по адресу почты (шаг перед приглашением).
func (c *Client) FindUser(ctx context.Context, email string) (Member, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return Member{}, errors.New("укажите почту")
	}
	raw, err := c.PostJSON(ctx, pathFindUser, map[string]interface{}{"email": email})
	if err != nil {
		return Member{}, err
	}
	value := asMap(raw["value"])
	if inner := asMap(value["value"]); inner != nil {
		value = inner
	}
	userID := str(value, "id")
	if userID == "" {
		return Member{}, errors.New("аккаунт с почтой " + email + " не найден")
	}
	name := strings.TrimSpace(str(value, "name"))
	if name == "" {
		name = email
	}
	return Member{UserID: userID, Name: name, Email: email, Avatar: str(value, "profile_photo")}, nil
}

// InviteInput — приглашение по списку уже известных userId и/или адресов почты.
type InviteInput struct {
	SpaceID string   `json:"spaceId"`
	UserIDs []string `json:"userIds"`
	Emails  []string `json:"emails"`
	Role    string   `json:"role"`
}

// InviteResult — что реально произошло, чтобы UI показал понятный итог.
type InviteResult struct {
	Invited []string `json:"invited"`
	Skipped []string `json:"skipped"`
}

// Invite добавляет людей в воркспейс. Уже состоящих участников пропускает,
// поэтому повторное приглашение того же человека невозможно.
func (c *Client) Invite(ctx context.Context, in InviteInput) (InviteResult, error) {
	var out InviteResult
	cfg, err := c.require()
	if err != nil {
		return out, err
	}
	spaceID := strings.TrimSpace(in.SpaceID)
	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	if spaceID == "" {
		return out, errors.New("не указан воркспейс")
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = "editor"
	}

	// Текущий состав — чтобы не приглашать повторно.
	existing := map[string]bool{}
	existingEmail := map[string]bool{}
	if members, err := c.Members(ctx, spaceID); err == nil {
		for _, member := range members {
			existing[member.UserID] = true
			if member.Email != "" {
				existingEmail[strings.ToLower(member.Email)] = true
			}
		}
	}

	ids := []string{}
	add := func(userID, label string) {
		if userID == "" {
			return
		}
		if existing[userID] {
			out.Skipped = append(out.Skipped, label+" — уже в воркспейсе")
			return
		}
		for _, known := range ids {
			if known == userID {
				return
			}
		}
		ids = append(ids, userID)
		out.Invited = append(out.Invited, label)
	}

	for _, userID := range in.UserIDs {
		add(strings.TrimSpace(userID), strings.TrimSpace(userID))
	}
	for _, email := range in.Emails {
		email = strings.TrimSpace(strings.ToLower(email))
		if email == "" {
			continue
		}
		if existingEmail[email] {
			out.Skipped = append(out.Skipped, email+" — уже в воркспейсе")
			continue
		}
		found, err := c.FindUser(ctx, email)
		if err != nil {
			out.Skipped = append(out.Skipped, email+" — "+err.Error())
			continue
		}
		add(found.UserID, email)
	}

	if len(ids) == 0 {
		return out, nil
	}

	invitees := make([]interface{}, 0, len(ids))
	for _, userID := range ids {
		invitees = append(invitees, map[string]interface{}{"userId": userID, "role": role})
	}
	if _, err := c.PostJSON(ctx, pathInviteGuests, map[string]interface{}{
		"action":  "create_invites",
		"spaceId": spaceID,
		"target":  map[string]interface{}{"table": "space", "id": spaceID},
		"invitees": invitees,
		"message":  "",
		"origin":   "space_switcher_invite_button",
	}); err != nil {
		return out, err
	}

	membership := "member"
	if role == "owner" || role == "admin" {
		membership = "owner"
	}
	userIDs := make([]interface{}, 0, len(ids))
	for _, userID := range ids {
		userIDs = append(userIDs, userID)
	}
	if _, err := c.PostJSON(ctx, pathAddMembers, map[string]interface{}{
		"spaceId":        spaceID,
		"userIds":        userIDs,
		"membershipType": membership,
		"billingExempt":  false,
	}); err != nil {
		return out, err
	}
	return out, nil
}

// RemoveMember исключает участника из воркспейса.
func (c *Client) RemoveMember(ctx context.Context, spaceID, userID string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
	}
	userID = strings.TrimSpace(userID)
	if spaceID == "" || userID == "" {
		return errors.New("не указан воркспейс или участник")
	}
	if userID == cfg.UserID {
		return errors.New("нельзя исключить самого себя")
	}
	_, err = c.PostJSON(ctx, pathRemoveMembers, map[string]interface{}{
		"spaceId": spaceID,
		"userIds": []interface{}{userID},
	})
	return err
}

// MemberCounts — сводка по типам участников (нужна для бейджей и лимитов).
func (c *Client) MemberCounts(ctx context.Context, spaceID string) (map[string]interface{}, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
	}
	return c.PostJSON(ctx, pathUserCounts, map[string]interface{}{"spaceId": spaceID})
}

// UpdateSpace меняет имя и/или иконку воркспейса транзакцией на таблицу space.
// Пустые поля не трогаем, чтобы случайно не стереть текущее значение.
func (c *Client) UpdateSpace(ctx context.Context, spaceID, name, icon string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
	}
	if spaceID == "" {
		return errors.New("не указан воркспейс")
	}
	args := map[string]interface{}{}
	if value := strings.TrimSpace(name); value != "" {
		args["name"] = value
	}
	if value := strings.TrimSpace(icon); value != "" {
		args["icon"] = value
	}
	if len(args) == 0 {
		return nil
	}
	return c.updateRecord(ctx, "space", spaceID, spaceID, args, "spaceSettings.update")
}

// UpdateAccount меняет профиль текущего аккаунта: имя и аватар.
func (c *Client) UpdateAccount(ctx context.Context, name, avatar string) error {
	cfg, err := c.require()
	if err != nil {
		return err
	}
	if cfg.UserID == "" {
		return errors.New("не известен пользователь — переимпортируйте cURL")
	}
	args := map[string]interface{}{}
	if value := strings.TrimSpace(name); value != "" {
		args["name"] = value
		parts := strings.Fields(value)
		args["given_name"] = parts[0]
		if len(parts) > 1 {
			args["family_name"] = strings.Join(parts[1:], " ")
		}
	}
	if value := strings.TrimSpace(avatar); value != "" {
		args["profile_photo"] = value
	}
	if len(args) == 0 {
		return nil
	}
	return c.updateRecord(ctx, "notion_user", cfg.UserID, cfg.SpaceID, args, "accountSettings.update")
}

// updateRecord — общая транзакция update для одной записи.
func (c *Client) updateRecord(ctx context.Context, table, id, spaceID string, args map[string]interface{}, userAction string) error {
	_, err := c.PostJSON(ctx, PathTransactions, map[string]interface{}{
		"requestId": uid.New(),
		"transactions": []interface{}{map[string]interface{}{
			"id":      uid.New(),
			"spaceId": spaceID,
			"debug": map[string]interface{}{
				"userAction":         userAction,
				"clientCommitTimeMs": time.Now().UnixMilli(),
			},
			"operations": []interface{}{map[string]interface{}{
				"pointer": map[string]interface{}{"table": table, "id": id, "spaceId": spaceID},
				"path":    []interface{}{},
				"command": "update",
				"args":    args,
			}},
		}},
	})
	return err
}
