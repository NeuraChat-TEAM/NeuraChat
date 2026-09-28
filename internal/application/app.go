package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"neura/internal/appcfg"
	"neura/internal/notcode"
	"neura/internal/notion"
	"neura/internal/store"
)

// App is the Wails-bound backend. Every exported method becomes a JS binding.
type App struct {
	ctx context.Context

	cfg     *appcfg.Store
	db      *store.Store
	client  *notion.Client
	runtime *notion.Runtime
	notcode *notcode.Manager
}

func NewApp() (*App, error) {
	cfg, err := appcfg.New()
	if err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.Dir())
	if err != nil {
		return nil, err
	}
	client := notion.NewClient()
	return &App{
		cfg:     cfg,
		db:      db,
		client:  client,
		runtime: notion.NewRuntime(client),
		notcode: notcode.New(),
	}, nil
}

// OnStartup restores the saved Notion session so the UI is usable immediately.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx

	// Связка локального чата с thread в Notion хранится в базе: без этого после
	// перезапуска продолжение старого чата уходило в новый thread.
	a.runtime.SetThreadStore(
		func(spaceID, conversationID, mode string) string {
			value, err := a.db.GetKV(threadMapKey(spaceID, conversationID, mode))
			if err == nil && strings.TrimSpace(value) != "" {
				return value
			}
			// Compatibility with mappings written before V2/V3 got explicit names.
			switch mode {
			case notion.ModeV2:
				value, _ = a.db.GetKV(threadMapKey(spaceID, conversationID, "legacy"))
			case notion.ModeV3:
				value, _ = a.db.GetKV(threadMapKey(spaceID, conversationID, "agent"))
			case notion.ModeLegacy:
				value, _ = a.db.GetKV(legacyThreadMapKey(spaceID, conversationID))
			}
			return value
		},
		func(spaceID, conversationID, mode, threadID string) error {
			return a.db.SetKV(threadMapKey(spaceID, conversationID, mode), threadID)
		},
	)

	// Сессии может не быть — настройки и автостарт всё равно применяем.
	// CaptureMode is authoritative because V2 and V3 need different captures.
	if saved, err := a.cfg.LoadSession(); err == nil && strings.TrimSpace(saved) != "" {
		if parsed, err := notion.ParseCurl(saved); err == nil {
			a.client.SetConfig(parsed)
		}
	}

	settings := a.cfg.LoadSettings()
	if cfg := a.client.Config(); cfg != nil {
		captureMode := notion.NormalizeChatMode(cfg.CaptureMode)
		if notion.NormalizeChatMode(settings.ChatMode) != captureMode {
			settings.ChatMode = captureMode
			settings.Model = ""
			settings.ReasoningEffort = ""
			_ = a.cfg.SaveSettings(settings)
		}
	}
	notion.SetMcpPathOverrides(settings.McpPaths)

	// Возвращаем последний выбранный воркспейс/аккаунт, иначе после
	// рестарта чат уходит в пространство из cURL, а не в то, что выбрал юзер.
	if strings.TrimSpace(settings.ActiveSpaceID) != "" {
		a.client.UseWorkspace(
			settings.ActiveUserID,
			settings.ActiveSpaceID,
			settings.ActiveSpaceViewID,
			settings.ActiveSpaceName,
		)
	}

	if settings.AutoStart {
		// Не передаём ctx окна: он отменяется после старта и обрывает ожидание.
		// Пути больше не спрашиваем у юзера — всё берётся из встроенной сборки.
		go func(s appcfg.Settings) {
			startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if _, err := a.notcode.Start(startCtx, notcodeOptions(s)); err != nil {
				a.emit(notion.Event{Type: "error", Label: "автозапуск notcode: " + err.Error()})
			}
		}(settings)
	}
}

// threadMapKey разводит связки по воркспейсам: один и тот же локальный чат
// в другом пространстве не должен цеплять чужой thread.
func threadMapKey(spaceID, conversationID, mode string) string {
	return "thread:" + mode + ":" + spaceID + ":" + conversationID
}

func legacyThreadMapKey(spaceID, conversationID string) string {
	return "thread:" + spaceID + ":" + conversationID
}

// OnShutdown stops child processes so no orphan bun/ngrok stays behind.
func (a *App) OnShutdown(context.Context) {
	a.notcode.Stop()
	a.client.Close()
	_ = a.db.Close()
}

func (a *App) emit(event notion.Event) {
	runtime.EventsEmit(a.ctx, "chat:event", event)
}

// ---------------------------------------------------------------- connection

// ConnectionState is what the Settings → Подключение tab renders.
type ConnectionState struct {
	Connected   bool   `json:"connected"`
	SpaceID     string `json:"spaceId"`
	UserID      string `json:"userId"`
	Origin      string `json:"origin"`
	CaptureMode string `json:"captureMode"`
}

func (a *App) ConnectionStatus() ConnectionState {
	cfg := a.client.Config()
	if cfg == nil {
		return ConnectionState{}
	}
	return ConnectionState{
		Connected: true, SpaceID: cfg.SpaceID, UserID: cfg.UserID,
		Origin: cfg.Origin, CaptureMode: cfg.CaptureMode,
	}
}

// ImportCurl validates and stores a "Copy as cURL" capture.
func (a *App) ImportCurl(source string) (ConnectionState, error) {
	parsed, err := notion.ParseCurl(source)
	if err != nil {
		return ConnectionState{}, err
	}
	profileID := strings.TrimSpace(parsed.UserID)
	if profileID == "" {
		profileID = parsed.SpaceID
	}
	if err := a.cfg.SaveSessionProfile(profileID, parsed.UserID, parsed.CaptureMode, source); err != nil {
		return ConnectionState{}, err
	}
	// Режим является частью импортированной browser-сессии. Раньше новый
	// createAgentThread-cURL сохранялся, но приложение оставалось в legacy и
	// после перезапуска пыталось читать пустой legacy template.
	settings := a.cfg.LoadSettings()
	if settings.ChatMode != parsed.CaptureMode {
		settings.ChatMode = parsed.CaptureMode
		settings.Model = ""
		settings.ReasoningEffort = ""
		if err := a.cfg.SaveSettings(settings); err != nil {
			return ConnectionState{}, err
		}
	}
	a.client.SetConfig(parsed)
	a.runtime.Reset()
	return a.ConnectionStatus(), nil
}

func (a *App) SessionProfiles() ([]appcfg.SessionProfile, error) {
	return a.cfg.SessionProfiles()
}

func (a *App) ActivateSession(id string) (ConnectionState, error) {
	source, err := a.cfg.ActivateSession(id)
	if err != nil {
		return ConnectionState{}, err
	}
	parsed, err := notion.ParseCurl(source)
	if err != nil {
		return ConnectionState{}, err
	}
	settings := a.cfg.LoadSettings()
	captureMode := notion.NormalizeChatMode(parsed.CaptureMode)
	if notion.NormalizeChatMode(settings.ChatMode) != captureMode {
		settings.ChatMode = captureMode
		settings.Model = ""
		settings.ReasoningEffort = ""
		if err := a.cfg.SaveSettings(settings); err != nil {
			return ConnectionState{}, err
		}
	}
	a.client.SetConfig(parsed)
	a.runtime.Reset()
	return a.ConnectionStatus(), nil
}

func (a *App) ClearSession() error {
	a.client.SetConfig(nil)
	a.runtime.Reset()
	return a.cfg.DeleteSession()
}

// ---------------------------------------------------------------- settings

func (a *App) GetSettings() appcfg.Settings { return a.cfg.LoadSettings() }

func (a *App) SaveSettings(next appcfg.Settings) (appcfg.Settings, error) {
	if err := a.cfg.SaveSettings(next); err != nil {
		return appcfg.Settings{}, err
	}
	saved := a.cfg.LoadSettings()
	notion.SetMcpPathOverrides(saved.McpPaths)
	return saved, nil
}

// McpEndpoints reports which private endpoint each MCP step resolved to.
func (a *App) McpEndpoints() map[string]string { return notion.ResolvedMcpPaths() }

// DefaultSystemPrompt is what the Settings tab pre-fills: the app's own MCP name.
func (a *App) DefaultSystemPrompt() string {
	settings := a.cfg.LoadSettings()
	name := settings.McpServerName
	if name == "" {
		name = "notcode"
	}
	return "use mcp " + name
}

// ---------------------------------------------------------------- debug

func (a *App) DebugLogs() []notion.DebugEntry { return a.client.DebugEntries() }

func (a *App) ClearDebugLogs() { a.client.ClearDebug() }

// ---------------------------------------------------------------- window

func (a *App) WindowMinimise() { runtime.WindowMinimise(a.ctx) }

func (a *App) WindowToggleMaximise() { runtime.WindowToggleMaximise(a.ctx) }

func (a *App) WindowClose() { runtime.Quit(a.ctx) }

func (a *App) OpenURL(target string) error {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return errors.New("разрешены только http/https ссылки")
	}
	runtime.BrowserOpenURL(a.ctx, target)
	return nil
}
