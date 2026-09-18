package application

import (
	"strings"

	"neura/internal/notion"
)

// Участники воркспейса, приглашения и настройки профиля для UI.

// InviteCandidate — аккаунт сессии, которого ещё нет в воркспейсе.
type InviteCandidate struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
}

// SpaceMembers — состав воркспейса и кандидаты на приглашение.
type SpaceMembers struct {
	SpaceID    string            `json:"spaceId"`
	SpaceName  string            `json:"spaceName"`
	Members    []notion.Member   `json:"members"`
	Candidates []InviteCandidate `json:"candidates"`
}

// ListMembers отдаёт участников воркспейса и те аккаунты сессии,
// которых там ещё нет — именно их показываем в диалоге приглашения.
func (a *App) ListMembers(spaceID string) (SpaceMembers, error) {
	out := SpaceMembers{SpaceID: spaceID, Members: []notion.Member{}, Candidates: []InviteCandidate{}}
	cfg := a.client.Config()
	if cfg == nil {
		return out, nil
	}
	if strings.TrimSpace(spaceID) == "" {
		spaceID = cfg.SpaceID
		out.SpaceID = spaceID
	}
	members, err := a.client.Members(a.ctx, spaceID)
	if err != nil {
		return out, err
	}
	out.Members = members

	inSpace := map[string]bool{}
	for _, member := range members {
		inSpace[member.UserID] = true
	}

	// Кандидаты — все аккаунты сессии без этого воркспейса.
	accounts, err := a.client.Accounts(a.ctx)
	if err != nil {
		return out, nil
	}
	seen := map[string]bool{}
	for _, account := range accounts {
		hasSpace := false
		for _, space := range account.Spaces {
			if space.ID == spaceID {
				hasSpace = true
			}
			if space.ID == spaceID && out.SpaceName == "" {
				out.SpaceName = space.Name
			}
		}
		if hasSpace || inSpace[account.UserID] || seen[account.UserID] {
			continue
		}
		seen[account.UserID] = true
		out.Candidates = append(out.Candidates, InviteCandidate{
			UserID: account.UserID,
			Name:   account.Name,
			Email:  account.Email,
			Avatar: account.Avatar,
		})
	}
	return out, nil
}

// InviteMembers приглашает выбранные аккаунты и/или адреса почты.
// Дубли отсеиваются внутри notion.Invite, а UI получает сразу новый состав.
func (a *App) InviteMembers(spaceID string, userIDs []string, emails []string, role string) (SpaceMembers, error) {
	if _, err := a.client.Invite(a.ctx, notion.InviteInput{
		SpaceID: spaceID,
		UserIDs: userIDs,
		Emails:  emails,
		Role:    role,
	}); err != nil {
		return SpaceMembers{}, err
	}
	return a.ListMembers(spaceID)
}

// RemoveMember исключает участника и возвращает обновлённый состав.
func (a *App) RemoveMember(spaceID, userID string) (SpaceMembers, error) {
	if err := a.client.RemoveMember(a.ctx, spaceID, userID); err != nil {
		return SpaceMembers{}, err
	}
	return a.ListMembers(spaceID)
}

// UpdateWorkspace меняет имя и аватарку воркспейса.
func (a *App) UpdateWorkspace(spaceID, name, icon string) (WorkspaceState, error) {
	if err := a.client.UpdateSpace(a.ctx, spaceID, name, icon); err != nil {
		return WorkspaceState{}, err
	}
	if strings.TrimSpace(name) != "" {
		settings := a.cfg.LoadSettings()
		if settings.ActiveSpaceID == spaceID || strings.TrimSpace(spaceID) == "" {
			settings.ActiveSpaceName = strings.TrimSpace(name)
			_ = a.cfg.SaveSettings(settings)
		}
	}
	return a.ListWorkspaces()
}

// UpdateAccount меняет имя и аватарку текущего аккаунта.
func (a *App) UpdateAccount(name, avatar string) (WorkspaceState, error) {
	if err := a.client.UpdateAccount(a.ctx, name, avatar); err != nil {
		return WorkspaceState{}, err
	}
	return a.ListWorkspaces()
}
