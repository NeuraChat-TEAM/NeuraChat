package application

import (
	"errors"
	"fmt"
	"strings"

	"neura/internal/appcfg"
	"neura/internal/notcode"
	"neura/internal/notion"
)

// notcodeOptions resolves the notcode/ngrok launch options, falling back to the
// copy of notcode that ships next to Neura.exe when the user left the field empty.
func notcodeOptions(settings appcfg.Settings) notcode.Options {
	return notcodeOptionsRotate(settings, true)
}

// notcodeOptionsRotate позволяет не перевыпускать Bearer-токен. Ротация
// перезаписывает ~/.notcode/config.json и рвёт живые MCP-сессии, поэтому
// для уже запущенного сервера мы токен переиспользуем.
func notcodeOptionsRotate(settings appcfg.Settings, rotate bool) notcode.Options {
	dir := strings.TrimSpace(settings.NotcodeDir)
	if dir == "" {
		dir = appcfg.DefaultNotcodeDir()
	}
	return notcode.Options{
		Dir:         dir,
		Runner:      settings.NotcodeCmd,
		Port:        settings.NotcodePort,
		NgrokPath:   settings.NgrokPath,
		NgrokDomain: settings.NgrokDomain,
		RotateToken: rotate,
	}
}

// liveNotcodeOptions не ротирует токен, если NotCode уже отвечает на /health.
func (a *App) liveNotcodeOptions(settings appcfg.Settings) notcode.Options {
	running := a.notcode.Status(a.ctx).NotcodeRunning
	opts := notcodeOptionsRotate(settings, !running)
	opts.NgrokToken, _ = a.cfg.LoadNgrokToken()
	return opts
}

type NgrokAuthState struct {
	Configured bool   `json:"configured"`
	Bundled    bool   `json:"bundled"`
	Path       string `json:"path"`
}

func (a *App) NgrokTokenStatus() NgrokAuthState {
	token, _ := a.cfg.LoadNgrokToken()
	path := appcfg.DefaultNgrokPath()
	return NgrokAuthState{Configured: strings.TrimSpace(token) != "", Bundled: strings.TrimSpace(path) != "ngrok", Path: path}
}

func (a *App) SaveNgrokToken(token string) (NgrokAuthState, error) {
	if err := a.cfg.SaveNgrokToken(token); err != nil {
		return a.NgrokTokenStatus(), err
	}
	return a.NgrokTokenStatus(), nil
}

func (a *App) ClearNgrokToken() (NgrokAuthState, error) {
	a.notcode.Stop()
	if err := a.cfg.DeleteNgrokToken(); err != nil {
		return a.NgrokTokenStatus(), err
	}
	return a.NgrokTokenStatus(), nil
}

// NotcodeLogs отдаёт разделённый по источникам лог для вкладки «Логи».
func (a *App) NotcodeLogs() []notcode.LogLine {
	return a.notcode.Logs()
}

// NotcodeClearLogs очищает буфер логов NotCode и ngrok.
func (a *App) NotcodeClearLogs() {
	a.notcode.ClearLogs()
}

// AIUsage возвращает rolling- и месячные лимиты AI по активному воркспейсу.
func (a *App) AIUsage() (notion.Usage, error) {
	settings := a.cfg.LoadSettings()
	return a.client.AIUsage(a.ctx, settings.ActiveSpaceID)
}

// NotcodeStatus reports notcode + ngrok state for the MCP settings tab.
func (a *App) NotcodeStatus() notcode.Status {
	return a.notcode.Status(a.ctx)
}

// NotcodeStart boots notcode and ngrok without touching Notion.
func (a *App) NotcodeStart() (notcode.Status, error) {
	settings := a.cfg.LoadSettings()
	opts := a.liveNotcodeOptions(settings)
	if strings.TrimSpace(opts.NgrokToken) == "" {
		return a.notcode.Status(a.ctx), errors.New("сначала сохраните ngrok authtoken")
	}
	return a.notcode.Start(a.ctx, opts)
}

func (a *App) NotcodeStop() notcode.Status {
	a.notcode.Stop()
	return a.notcode.Status(a.ctx)
}

// ConnectNotcodeResult is what the one-click button returns.
type ConnectNotcodeResult struct {
	Status notcode.Status    `json:"status"`
	Module *notion.McpModule `json:"module"`
}

// ConnectNotcodeMcp is the single button the user asked for:
// start notcode -> start ngrok http <port> -> read the Bearer token from
// ~/.notcode/config.json -> register the tunnel as an MCP server in Notion.
func (a *App) ConnectNotcodeMcp() (ConnectNotcodeResult, error) {
	settings := a.cfg.LoadSettings()

	opts := a.liveNotcodeOptions(settings)
	if strings.TrimSpace(opts.NgrokToken) == "" {
		return ConnectNotcodeResult{}, errors.New("сначала сохраните ngrok authtoken")
	}
	status, err := a.notcode.Start(a.ctx, opts)
	if err != nil {
		return ConnectNotcodeResult{Status: status}, err
	}

	// Путь MCP берём из ~/.notcode/config.json (sse.mcpPath): модифицированный
	// notcode умеет переименовывать маршрут (notcode tools mcp <path>),
	// а жёстко зашитый адрес и давал 404.
	mcpPath := status.McpPath
	if strings.TrimSpace(mcpPath) == "" {
		mcpPath = notcode.DefaultMcpPath
	}
	serverURL := strings.TrimRight(status.PublicURL, "/") + mcpPath
	// У встроенного сервера одно стабильное имя. Раньше к нему добавлялся
	// хвост токена, поэтому после каждого переподключения Notion и список
	// интеграций копили notcode-xxxxxx.
	name := "notcode"
	previousModules, _ := a.client.ListMcp(a.ctx)

	// Проверяем туннель и токен ДО регистрации в Notion:
	// иначе Notion сохраняет нерабочий адрес и падает позже, без объяснений.
	if err := notcode.VerifyTunnel(a.ctx, status.PublicURL, status.Token); err != nil {
		return ConnectNotcodeResult{Status: status}, err
	}

	module, err := a.client.ConnectMcp(a.ctx, notion.ConnectMcpInput{
		Name:      name,
		ServerURL: serverURL,
		Token:     status.Token,
		AutoWrite: false,
	})
	if err != nil {
		// Частичный успех (сервер создан, но не включён) тоже возвращаем в UI.
		return ConnectNotcodeResult{Status: status, Module: module}, err
	}

	// Новый модуль уже проверен и подключён — теперь безопасно удаляем прежние
	// встроенные NotCode-модули. Если подключение нового упало, старый остаётся.
	for _, old := range previousModules {
		oldName := strings.ToLower(strings.TrimSpace(old.Name))
		if old.IntegrationID == module.IntegrationID || (oldName != "notcode" && !strings.HasPrefix(oldName, "notcode-")) {
			continue
		}
		if err := a.client.DisconnectMcp(a.ctx, old.IntegrationID); err == nil {
			settings.McpServers = appcfg.ForgetMcpServer(settings.McpServers, old.Name, old.ServerURL)
		}
	}

	// Remember the module so it can be disconnected later, and pre-fill the
	// system prompt with the name the app actually registered.
	settings.McpIntegrationID = module.IntegrationID
	settings.McpServerURL = serverURL
	settings.McpServerName = module.Name
	settings.McpServers = appcfg.RememberMcpServer(settings.McpServers, appcfg.McpServer{
		Name:      module.Name,
		ServerURL: serverURL,
		Token:     status.Token,
		AutoWrite: false,
	})
	if strings.TrimSpace(settings.SystemPrompt) == "" {
		settings.SystemPrompt = "use mcp " + module.Name
	}
	if err := a.cfg.SaveSettings(settings); err != nil {
		return ConnectNotcodeResult{Status: status, Module: module}, fmt.Errorf("MCP подключён, но настройки не сохранились: %w", err)
	}

	return ConnectNotcodeResult{Status: status, Module: module}, nil
}

// McpList returns MCP servers attached to the current Notion space.
func (a *App) McpList() ([]notion.McpModule, error) {
	return a.client.ListMcp(a.ctx)
}

// McpConnect adds an arbitrary MCP server (manual "Add MCP" form).
func (a *App) McpConnect(in notion.ConnectMcpInput) (*notion.McpModule, error) {
	if strings.TrimSpace(in.ServerURL) == "" {
		return nil, errors.New("укажите адрес MCP-сервера")
	}
	module, err := a.client.ConnectMcp(a.ctx, in)
	if err != nil {
		return nil, err
	}
	settings := a.cfg.LoadSettings()
	settings.McpIntegrationID = module.IntegrationID
	settings.McpServerURL = module.ServerURL
	settings.McpServerName = module.Name
	settings.McpServers = appcfg.RememberMcpServer(settings.McpServers, appcfg.McpServer{
		Name:      module.Name,
		ServerURL: module.ServerURL,
		Token:     in.Token,
		AutoWrite: in.AutoWrite,
		// Без этого ключи из query/заголовков терялись после рестарта,
		// и в новом воркспейсе Smithery снова отвечал отказом.
		Headers: in.Headers,
		Query:   in.Query,
		AutoRun: in.AutoRun,
	})
	if err := a.cfg.SaveSettings(settings); err != nil {
		return module, fmt.Errorf("MCP подключён, но настройки не сохранились: %w", err)
	}
	return module, nil
}

// McpStdioInput — установка локального (stdio) MCP-сервера: npx/uvx-пакета
type McpStdioInput struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Cwd       string            `json:"cwd"`
	AutoRun   bool              `json:"autoRun"`
	AutoWrite bool              `json:"runWriteToolsAutomatically"`
}

// McpInstallStdio подключает к Notion локальный MCP-сервер (например
// npx -y @playwright/mcp@latest). Notion умеет только сетевые эндпоинты,
// поэтому пакет запускает notcode и отдаёт его как /bridge/<slug>/mcp
// в том же ngrok-туннеле и под тем же Bearer-токеном.
func (a *App) McpInstallStdio(in McpStdioInput) (*notion.McpModule, error) {
	name := strings.TrimSpace(in.Name)
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return nil, errors.New("укажите команду запуска (например npx)")
	}
	if name == "" {
		return nil, errors.New("укажите имя сервера")
	}
	slug := notcode.Slug(name)

	// Без живого туннеля мосту некуда смотреть — поднимаем notcode сами.
	settings := a.cfg.LoadSettings()
	opts := a.liveNotcodeOptions(settings)
	if strings.TrimSpace(opts.NgrokToken) == "" {
		return nil, errors.New("сначала сохраните ngrok authtoken")
	}
	status, err := a.notcode.Start(a.ctx, opts)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status.PublicURL) == "" {
		return nil, errors.New("ngrok ещё не выдал публичный адрес")
	}

	spec := notcode.BridgeSpec{Command: command, Args: in.Args, Env: in.Env, Cwd: strings.TrimSpace(in.Cwd)}
	if err := notcode.SetBridge(slug, spec); err != nil {
		return nil, fmt.Errorf("не удалось записать мост в конфиг notcode: %w", err)
	}

	// Проверяем рукопожатие до регистрации: первый запуск npx может тянуть
	// пакет из сети, и пусть ошибка всплывёт здесь, а не в чате Notion.
	if err := notcode.VerifyBridge(a.ctx, status.PublicURL, slug, status.Token); err != nil {
		_ = notcode.RemoveBridge(slug)
		return nil, fmt.Errorf("локальный MCP-сервер не ответил: %w", err)
	}

	serverURL := notcode.BridgeURL(status.PublicURL, slug)
	module, err := a.client.ConnectMcp(a.ctx, notion.ConnectMcpInput{
		Name:      name,
		ServerURL: serverURL,
		Token:     status.Token,
		AutoWrite: in.AutoWrite,
		AutoRun:   in.AutoRun,
	})
	if err != nil {
		return module, err
	}

	settings = a.cfg.LoadSettings()
	settings.McpServers = appcfg.RememberMcpServer(settings.McpServers, appcfg.McpServer{
		Name:      name,
		ServerURL: serverURL,
		Token:     status.Token,
		AutoWrite: in.AutoWrite,
		AutoRun:   in.AutoRun,
		Transport: "stdio",
		Command:   command,
		Args:      in.Args,
		Env:       in.Env,
	})
	if err := a.cfg.SaveSettings(settings); err != nil {
		return module, fmt.Errorf("MCP подключён, но настройки не сохранились: %w", err)
	}
	return module, nil
}

// McpDisconnect removes an MCP server from the space.
func (a *App) McpDisconnect(integrationID string) error {
	// Адрес читаем ДО отключения: потом модуля уже не будет в space.
	removedName, removedURL := "", ""
	if modules, err := a.client.ListMcp(a.ctx); err == nil {
		for _, module := range modules {
			if module.IntegrationID == integrationID {
				removedName, removedURL = module.Name, module.ServerURL
			}
		}
	}
	if err := a.client.DisconnectMcp(a.ctx, integrationID); err != nil {
		return err
	}
	settings := a.cfg.LoadSettings()
	// Больше не поднимаем его автоматически в новых воркспейсах.
	if removedName != "" || removedURL != "" {
		// У stdio-сервера есть ещё и мост в конфиге notcode: без удаления
		// дочерний процесс продолжал бы подниматься по запросу.
		for _, server := range settings.McpServers {
			if server.Transport != "stdio" {
				continue
			}
			if strings.EqualFold(server.Name, removedName) || (removedURL != "" && server.ServerURL == removedURL) {
				_ = notcode.RemoveBridge(notcode.Slug(server.Name))
			}
		}
		settings.McpServers = appcfg.ForgetMcpServer(settings.McpServers, removedName, removedURL)
		if err := a.cfg.SaveSettings(settings); err != nil {
			return fmt.Errorf("отключено, но настройки не сохранились: %w", err)
		}
	}
	if settings.McpIntegrationID == integrationID {
		settings.McpIntegrationID = ""
		settings.McpServerURL = ""
		if err := a.cfg.SaveSettings(settings); err != nil {
			return fmt.Errorf("отключено, но настройки не сохранились: %w", err)
		}
	}
	return nil
}

// McpSetEnabled flips one module on or off, keeping the rest untouched.
func (a *App) McpSetEnabled(integrationID string, enabled bool) error {
	if strings.TrimSpace(integrationID) == "" {
		return errors.New("не указан модуль")
	}
	modules, err := a.client.ListMcp(a.ctx)
	if err != nil {
		return err
	}
	found := false
	for i := range modules {
		if modules[i].IntegrationID == integrationID {
			modules[i].Enabled = enabled
			found = true
		}
	}
	if !found {
		return errors.New("модуль не найден в этом пространстве")
	}
	return a.client.SetMcpEnabled(a.ctx, "", modules)
}

// baseServerURL отбрасывает query: один и тот же сервер подключён с ключом в
// параметрах, а в настройках может лежать чистый адрес — без нормализации
// проверка дублей не срабатывала и сервер подключался второй раз.
func baseServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if index := strings.IndexByte(raw, '?'); index >= 0 {
		raw = raw[:index]
	}
	return strings.TrimRight(raw, "/")
}

// ReconnectMcpServers заново подключает все известные MCP-серверы к активному
// воркспейсу. В Notion workflow_module живёт внутри space, поэтому при
// создании или смене воркспейса подключения не переезжают сами.
func (a *App) ReconnectMcpServers() error {
	settings := a.cfg.LoadSettings()
	if len(settings.McpServers) == 0 {
		return nil
	}

	// Актуальный адрес туннеля notcode важнее сохранённого: ngrok меняет URL.
	status := a.notcode.Status(a.ctx)
	notcodeURL := ""
	if strings.TrimSpace(status.PublicURL) != "" {
		mcpPath := status.McpPath
		if strings.TrimSpace(mcpPath) == "" {
			mcpPath = notcode.DefaultMcpPath
		}
		notcodeURL = strings.TrimRight(status.PublicURL, "/") + mcpPath
	}

	// Уже подключённые в этом space серверы пропускаем, чтобы не плодить дубли.
	existing := map[string]bool{}
	if modules, err := a.client.ListMcp(a.ctx); err == nil {
		for _, module := range modules {
			existing[baseServerURL(module.ServerURL)] = true
		}
	}

	var failures []string
	changed := false
	for i := range settings.McpServers {
		server := settings.McpServers[i]
		if strings.TrimSpace(server.ServerURL) == "" && notcodeURL == "" {
			continue
		}
		// stdio-серверы живут внутри того же туннеля: адрес и токен пересчитываем,
		// а описание моста восстанавливаем — конфиг notcode мог быть сброшен.
		if server.Transport == "stdio" {
			if notcodeURL == "" || strings.TrimSpace(status.PublicURL) == "" {
				continue
			}
			slug := notcode.Slug(server.Name)
			_ = notcode.SetBridge(slug, notcode.BridgeSpec{Command: server.Command, Args: server.Args, Env: server.Env})
			server.ServerURL = notcode.BridgeURL(status.PublicURL, slug)
			if strings.TrimSpace(status.Token) != "" {
				server.Token = status.Token
			}
		} else if notcodeURL != "" && strings.EqualFold(server.Name, settings.McpServerName) {
			server.ServerURL = notcodeURL
			if strings.TrimSpace(status.Token) != "" {
				server.Token = status.Token
			}
		}
		if existing[baseServerURL(server.ServerURL)] {
			continue
		}
		module, err := a.client.ConnectMcp(a.ctx, notion.ConnectMcpInput{
			Name:      server.Name,
			ServerURL: server.ServerURL,
			Token:     server.Token,
			AutoWrite: server.AutoWrite,
			Headers:   server.Headers,
			Query:     server.Query,
			AutoRun:   server.AutoRun,
		})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", server.Name, err))
			continue
		}
		settings.McpServers[i] = server
		changed = true
		if module != nil {
			settings.McpIntegrationID = module.IntegrationID
			settings.McpServerURL = module.ServerURL
		}
	}
	if changed {
		if err := a.cfg.SaveSettings(settings); err != nil {
			return fmt.Errorf("MCP переподключены, но настройки не сохранились: %w", err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("не все MCP поднялись в новом воркспейсе: %s", strings.Join(failures, "; "))
	}
	return nil
}
