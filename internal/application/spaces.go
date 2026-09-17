package application

import (
	"strings"

	"neura/internal/notion"
)

// Переключатель аккаунтов и воркспейсов в шапке сайдбара.

// WorkspaceState — всё, что нужно попапу: список аккаунтов с воркспейсами
// и текущий выбор.
type WorkspaceState struct {
	Accounts      []notion.Account `json:"accounts"`
	ActiveUserID  string           `json:"activeUserId"`
	ActiveSpaceID string           `json:"activeSpaceId"`
	ActiveName    string           `json:"activeName"`
	ActivePlan    string           `json:"activePlan"`
	CanCreate     bool             `json:"canCreate"`
}

// ListWorkspaces собирает аккаунты текущей сессии и их воркспейсы.
func (a *App) ListWorkspaces() (WorkspaceState, error) {
	cfg := a.client.Config()
	if cfg == nil {
		return WorkspaceState{Accounts: []notion.Account{}}, nil
	}
	accounts, err := a.client.Accounts(a.ctx)
	if err != nil {
		return WorkspaceState{}, err
	}

	state := WorkspaceState{
		Accounts:      accounts,
		ActiveUserID:  cfg.UserID,
		ActiveSpaceID: cfg.SpaceID,
	}
	for _, account := range accounts {
		for _, space := range account.Spaces {
			if space.ID == cfg.SpaceID {
				state.ActiveName = space.Name
				state.ActivePlan = space.PlanType
			}
		}
	}
	// Право на создание — мягкая проверка: ошибка не должна ломать попап.
	if allowed, err := a.client.CanCreateSpace(a.ctx); err == nil {
		state.CanCreate = allowed
	}
	return state, nil
}

// SwitchWorkspace делает выбранный воркспейс активным и запоминает выбор.
func (a *App) SwitchWorkspace(userID, spaceID, spaceViewID, spaceName string) (WorkspaceState, error) {
	if err := a.client.UseWorkspace(userID, spaceID, spaceViewID, spaceName); err != nil {
		return WorkspaceState{}, err
	}
	// Старые треды привязаны к предыдущему пространству.
	a.runtime.Reset()

	settings := a.cfg.LoadSettings()
	settings.ActiveUserID = userID
	settings.ActiveSpaceID = spaceID
	settings.ActiveSpaceViewID = spaceViewID
	settings.ActiveSpaceName = spaceName
	_ = a.cfg.SaveSettings(settings)

	state, err := a.ListWorkspaces()
	if err != nil {
		return state, err
	}
	// workflow_module привязан к space: после каждого перехода проверяем
	// сохранённые MCP и добавляем только отсутствующие в выбранный workspace.
	if err := a.ReconnectMcpServers(); err != nil {
		return state, err
	}
	return state, nil
}

// CreateWorkspace создаёт новый воркспейс и сразу переключается на него.
func (a *App) CreateWorkspace(name string) (WorkspaceState, error) {
	space, err := a.client.CreateSpace(a.ctx, strings.TrimSpace(name))
	if err != nil {
		return WorkspaceState{}, err
	}
	userID := ""
	if cfg := a.client.Config(); cfg != nil {
		userID = cfg.UserID
	}
	// SwitchWorkspace уже синхронизирует сохранённые MCP. Повторный вызов здесь
	// создавал гонку и мог регистрировать один сервер дважды.
	return a.SwitchWorkspace(userID, space.ID, space.SpaceViewID, space.Name)
}
