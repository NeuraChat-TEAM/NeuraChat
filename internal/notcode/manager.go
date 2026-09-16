package notcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogLine — одна строка лога с источником, чтобы UI мог разделить
// вывод notcode, ngrok и самого менеджера по вкладкам.
type LogLine struct {
	Time   string `json:"time"`
	Source string `json:"source"` // app | notcode | ngrok
	Level  string `json:"level"`  // info | warn | error
	Text   string `json:"text"`
}

// Status is surfaced in the MCP settings tab.
type Status struct {
	NotcodeRunning bool   `json:"notcodeRunning"`
	NgrokRunning   bool   `json:"ngrokRunning"`
	Token          string `json:"token"`
	LocalURL       string `json:"localUrl"`
	PublicURL      string `json:"publicUrl"`
	McpURL         string `json:"mcpUrl"`
	WorkspaceRoot  string `json:"workspaceRoot"`
	McpPath        string `json:"mcpPath"`
	Mode           string `json:"mode"`
	Port           int    `json:"port"`
	NotcodePid     int    `json:"notcodePid"`
	NgrokPid       int    `json:"ngrokPid"`
	Managed        bool   `json:"managed"` // процессы запущены этим приложением
	Version        string `json:"version"`
	Tools          int    `json:"tools"`
	Sessions       int    `json:"sessions"`
	UptimeSec      int    `json:"uptimeSec"`
	ConfigFile     string `json:"configFile"`
	NgrokDashboard string `json:"ngrokDashboard"`
	StartedAt      int64  `json:"startedAt"`

	// TunnelOK — публичный URL действительно доводит до NotCode.
	// Раньше UI считал ngrok «работает» просто по наличию туннеля.
	TunnelOK    bool   `json:"tunnelOk"`
	TunnelError string `json:"tunnelError,omitempty"`

	Log      string    `json:"log"`
	LogLines []LogLine `json:"logLines"`
	Error    string    `json:"error,omitempty"`
}

// Options configures one start attempt.
type Options struct {
	Dir         string // notcode checkout (contains package.json)
	Runner      string // bun (default) or npm/node
	Port        int
	NgrokPath   string
	NgrokDomain string
	// RotateToken — перегенерировать Bearer-токен перед запуском notcode.
	// Работает только когда сервер поднимаем мы: у чужого процесса токен
	// уже в памяти, и перезапись конфига разорвала бы активные сессии.
	RotateToken bool
}

type config struct {
	Token string `json:"token"`
	Mode  string `json:"mode"`
	// notcode пишет порт и хост в корень config.json; вложенный "server"
	// остался от старого формата.
	Port   int    `json:"port"`
	Host   string `json:"host"`
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	WorkspaceRoot string `json:"workspaceRoot"`
	ActiveProfile string `json:"activeProfile"`
	Profiles      []struct {
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"profiles"`
	SSE struct {
		McpPath      string `json:"mcpPath"`
		SsePath      string `json:"ssePath"`
		MessagesPath string `json:"messagesPath"`
	} `json:"sse"`
}

// ListenPort возвращает порт, на котором notcode действительно слушает.
func (c config) ListenPort() int {
	if c.Port > 0 {
		return c.Port
	}
	if c.Server.Port > 0 {
		return c.Server.Port
	}
	return 0
}

// Root возвращает корень активного профиля: в новом notcode workspaceRoot
// живёт внутри profiles[], а не в корне конфига.
func (c config) Root() string {
	for _, p := range c.Profiles {
		if p.Name == c.ActiveProfile && strings.TrimSpace(p.Root) != "" {
			return p.Root
		}
	}
	if strings.TrimSpace(c.WorkspaceRoot) != "" {
		return c.WorkspaceRoot
	}
	if len(c.Profiles) > 0 {
		return c.Profiles[0].Root
	}
	return ""
}

// DefaultMcpPath is notcode's primary streamable-HTTP MCP route.
const DefaultMcpPath = "/mcp"

// SSEPath is the legacy SSE transport route.
const SSEPath = "/sse"

// McpPath returns the MCP route notcode is actually serving.
func McpPath() string {
	cfg, err := ReadConfig()
	if err != nil {
		return DefaultMcpPath
	}
	return normalizePath(cfg.SSE.McpPath, DefaultMcpPath)
}

func normalizePath(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimRight(value, "/")
}

// EmbeddedDirName — папка с встроенным notcode, которая поставляется вместе
// с приложением.
const EmbeddedDirName = "notcode-embedded"

// ResolveDir ищет рабочую копию notcode.
func ResolveDir(dir string) string {
	if isNotcodeDir(dir) {
		return dir
	}
	var roots []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		roots = append(roots, exeDir, filepath.Join(exeDir, ".."), filepath.Join(exeDir, "..", ".."))
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd, filepath.Join(wd, ".."), filepath.Join(wd, "..", ".."))
	}
	names := []string{EmbeddedDirName, "notcode"}
	for _, root := range roots {
		for _, name := range names {
			if candidate := filepath.Join(root, name); isNotcodeDir(candidate) {
				return candidate
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range names {
			if candidate := filepath.Join(home, ".notcode", name); isNotcodeDir(candidate) {
				return candidate
			}
		}
	}
	return strings.TrimSpace(dir)
}

func isNotcodeDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "package.json"))
	return err == nil
}

// Manager owns the notcode and ngrok child processes.
type Manager struct {
	mu        sync.Mutex
	notcode   *exec.Cmd
	ngrok     *exec.Cmd
	log       []LogLine
	public    string
	opts      Options
	startedAt time.Time
}

func New() *Manager { return &Manager{} }

func (m *Manager) appendLine(source, level, format string, args ...interface{}) {
	line := LogLine{
		Time:   time.Now().Format(time.RFC3339),
		Source: source,
		Level:  level,
		Text:   fmt.Sprintf(format, args...),
	}
	m.mu.Lock()
	m.log = append(m.log, line)
	if len(m.log) > 1000 {
		m.log = m.log[len(m.log)-1000:]
	}
	m.mu.Unlock()
}

func (m *Manager) appendLog(format string, args ...interface{}) {
	m.appendLine("app", "info", format, args...)
}

// Logs возвращает структурированный лог для вкладки «Логи».
func (m *Manager) Logs() []LogLine {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogLine, len(m.log))
	copy(out, m.log)
	return out
}

// ClearLogs очищает буфер логов.
func (m *Manager) ClearLogs() {
	m.mu.Lock()
	m.log = nil
	m.mu.Unlock()
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".notcode", "config.json")
}

// ConfigFile — путь к конфигу notcode (для UI).
func ConfigFile() string { return configPath() }

// ReadConfig loads ~/.notcode/config.json (token, mode, port).
func ReadConfig() (config, error) {
	var out config
	path := configPath()
	if path == "" {
		return out, errors.New("не удалось определить домашнюю папку")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

// newToken — свежий Bearer-токен на запуск.
func newToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("notcode-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// writeConfigFields мягко правит ~/.notcode/config.json: читает JSON как
// карту, меняет только нужные ключи и пишет обратно. Так не теряются
// профили, лимиты и всё остальное, чего Go-структура не знает.
func writeConfigFields(fields map[string]interface{}) error {
	path := configPath()
	if path == "" {
		return errors.New("не удалось определить домашнюю папку")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	doc := map[string]interface{}{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	for key, value := range fields {
		doc[key] = value
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PrepareToken пишет в конфиг новый токен и порт перед запуском notcode.
// Возвращает токен, который сервер прочитает при старте.
func (m *Manager) PrepareToken(port int) (string, error) {
	token := newToken()
	fields := map[string]interface{}{"token": token}
	if port > 0 {
		fields["port"] = port
		fields["host"] = "127.0.0.1"
	}
	if err := writeConfigFields(fields); err != nil {
		return "", fmt.Errorf("не удалось записать токен в %s: %w", configPath(), err)
	}
	m.appendLog("сгенерирован новый Bearer-токен для notcode")
	return token, nil
}

func (m *Manager) alive(cmd *exec.Cmd) bool {
	return cmd != nil && cmd.Process != nil && (cmd.ProcessState == nil || !cmd.ProcessState.Exited())
}

type statusPayload struct {
	Version   string `json:"version"`
	Mode      string `json:"mode"`
	Tools     int    `json:"tools"`
	Sessions  int    `json:"sessions"`
	Root      string `json:"workspaceRoot"`
	UptimeSec int    `json:"uptimeSec"`
}

// probeStatus дергает /status с токеном — оттуда берём версию, режим,
// количество тулов и живых сессий для карточек в настройках.
func probeStatus(ctx context.Context, base, token string) (statusPayload, error) {
	var out statusPayload
	if strings.TrimSpace(token) == "" {
		return out, errors.New("нет токена")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/status", nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("ngrok-skip-browser-warning", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// Status reports the current state, probing /health for liveness.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	opts := m.opts
	public := m.public
	lines := make([]LogLine, len(m.log))
	copy(lines, m.log)
	notcodeAlive := m.alive(m.notcode)
	ngrokAlive := m.alive(m.ngrok)
	notcodePid, ngrokPid := 0, 0
	if notcodeAlive {
		notcodePid = m.notcode.Process.Pid
	}
	if ngrokAlive {
		ngrokPid = m.ngrok.Process.Pid
	}
	startedAt := m.startedAt
	m.mu.Unlock()

	port := opts.Port
	if port == 0 {
		port = 3000
	}

	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		stamp := line.Time
		if parsed, err := time.Parse(time.RFC3339, stamp); err == nil {
			stamp = parsed.Format("15:04:05")
		}
		plain = append(plain, stamp+"  "+line.Text)
	}

	mcpPath := DefaultMcpPath
	out := Status{
		NgrokRunning:   ngrokAlive || public != "",
		PublicURL:      public,
		Log:            strings.Join(plain, "\n"),
		LogLines:       lines,
		NotcodePid:     notcodePid,
		NgrokPid:       ngrokPid,
		Managed:        notcodeAlive,
		ConfigFile:     configPath(),
		NgrokDashboard: "http://127.0.0.1:4040",
	}
	if !startedAt.IsZero() {
		out.StartedAt = startedAt.UnixMilli()
	}
	cfgPort := 0
	if cfg, err := ReadConfig(); err == nil {
		out.Token = cfg.Token
		out.Mode = cfg.Mode
		out.WorkspaceRoot = cfg.Root()
		mcpPath = normalizePath(cfg.SSE.McpPath, DefaultMcpPath)
		cfgPort = cfg.ListenPort()
	}

	local := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthErr := probeHealthErr(ctx, local)
	if healthErr != nil && cfgPort != 0 && cfgPort != port {
		altLocal := fmt.Sprintf("http://127.0.0.1:%d", cfgPort)
		if altErr := probeHealthErr(ctx, altLocal); altErr == nil {
			port, local, healthErr = cfgPort, altLocal, nil
		}
	}
	out.Port = port
	// Живость определяет только /health. Раньше сюда входил notcodeAlive,
	// и упавший дочерний процесс всё равно показывался как «запущен».
	out.NotcodeRunning = healthErr == nil
	if healthErr != nil {
		// Человеческий текст вместо сырой сетевой ошибки: раньше в UI
		// прилетало «127.0.0.1:3000/health: dial tcp …» и было непонятно.
		out.Error = describeHealthError(local, healthErr)
	}
	out.McpPath = mcpPath
	out.LocalURL = local + mcpPath
	if out.NotcodeRunning {
		if info, err := probeStatus(ctx, local, out.Token); err == nil {
			out.Version = info.Version
			out.Tools = info.Tools
			out.Sessions = info.Sessions
			out.UptimeSec = info.UptimeSec
			if info.Mode != "" {
				out.Mode = info.Mode
			}
			if info.Root != "" {
				out.WorkspaceRoot = info.Root
			}
		}
	}
	if public == "" {
		if url, err := ngrokPublicURL(ctx, port); err == nil {
			out.PublicURL = url
			out.NgrokRunning = true
		}
	}
	if out.PublicURL != "" {
		base := strings.TrimRight(out.PublicURL, "/")
		out.McpURL = base + mcpPath
		if err := probeHealthErr(ctx, base); err != nil {
			out.TunnelError = fmt.Sprintf("туннель %s не доводит до NotCode: %v", base, err)
		} else {
			out.TunnelOK = true
		}
		out.NgrokRunning = out.TunnelOK || ngrokAlive
	}
	return out
}

// describeHealthError переводит причину недоступности в понятный текст
// с подсказкой, что делать.
func describeHealthError(local string, err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connectex"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "No connection could be made"), strings.Contains(msg, "актив"):
		return fmt.Sprintf("NotCode не отвечает на %s — сервер не запущен. Нажмите «Запустить инструменты».", local)
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return fmt.Sprintf("NotCode на %s не ответил за 3 секунды — возможно, ещё запускается.", local)
	case strings.Contains(msg, "401"):
		return "NotCode отклонил Bearer-токен — перезапустите инструменты, чтобы выдать новый."
	default:
		return fmt.Sprintf("NotCode на %s недоступен: %s", local, msg)
	}
}

func probeHealth(ctx context.Context, base string) bool {
	return probeHealthErr(ctx, base) == nil
}

// probeHealthErr возвращает причину, по которой /health не подтвердил notcode.
func probeHealthErr(ctx context.Context, base string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("ngrok-skip-browser-warning", "true")
	req.Header.Set("User-Agent", "neura-health/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(string(body)))
	}
	if err := checkNgrokBody(body); err != nil {
		return err
	}
	if !strings.Contains(string(body), "\"status\"") {
		return fmt.Errorf("неожиданный ответ: %s", snippet(string(body)))
	}
	return nil
}

func snippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 200 {
		return text[:200] + "…"
	}
	return text
}

// checkNgrokBody ловит HTML-заглушку ngrok.
func checkNgrokBody(body []byte) error {
	text := string(body)
	if strings.Contains(text, "ERR_NGROK") {
		return fmt.Errorf("ngrok отдал свою страницу вместо notcode: %s", snippet(text))
	}
	if strings.Contains(strings.ToLower(text), "<html") {
		return fmt.Errorf("вместо JSON notcode пришёл HTML: %s", snippet(text))
	}
	return nil
}

// portBusy сообщает, занят ли локальный порт кем-то ещё.
func portBusy(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Start boots notcode (if needed), then ngrok, and returns the resolved status.
func (m *Manager) Start(ctx context.Context, opts Options) (Status, error) {
	if opts.Port == 0 {
		opts.Port = 3000
	}
	if opts.Runner == "" {
		opts.Runner = "bun"
	}
	if opts.NgrokPath == "" {
		opts.NgrokPath = "ngrok"
	}
	opts.Dir = ResolveDir(opts.Dir)
	m.mu.Lock()
	m.opts = opts
	m.mu.Unlock()

	// Уже запущенный вручную notcode слушает порт из ~/.notcode/config.json.
	if cfg, err := ReadConfig(); err == nil {
		if cfgPort := cfg.ListenPort(); cfgPort != 0 && cfgPort != opts.Port {
			if probeHealth(ctx, fmt.Sprintf("http://127.0.0.1:%d", cfgPort)) {
				m.appendLog("notcode уже слушает порт %d из %s", cfgPort, configPath())
				opts.Port = cfgPort
				m.mu.Lock()
				m.opts = opts
				m.mu.Unlock()
			}
		}
	}

	local := fmt.Sprintf("http://127.0.0.1:%d", opts.Port)
	if healthErr := probeHealthErr(ctx, local); healthErr != nil {
		if portBusy(opts.Port) {
			m.appendLine("app", "warn", "порт %d занят другим процессом — notcode не поднять", opts.Port)
			return m.Status(ctx), fmt.Errorf("порт %d занят другим процессом: освободите его или укажите другой порт в настройках", opts.Port)
		}
		m.appendLog("notcode не отвечает на %s/health — запускаю", local)
		// Свежий токен на каждый запуск: его увидит и notcode, и Notion.
		if opts.RotateToken {
			if _, err := m.PrepareToken(opts.Port); err != nil {
				m.appendLine("app", "warn", "%v", err)
			}
		}
		if err := m.startNotcode(opts); err != nil {
			return m.Status(ctx), err
		}
		if err := m.waitHealth(ctx, local, 60*time.Second); err != nil {
			return m.Status(ctx), err
		}
	} else {
		m.appendLog("notcode уже слушает %s — использую его токен", local)
	}

	if err := m.startNgrok(ctx, opts); err != nil {
		return m.Status(ctx), err
	}

	m.mu.Lock()
	if m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}
	m.mu.Unlock()

	status := m.Status(ctx)
	if status.Token == "" {
		return status, fmt.Errorf("в %s нет Bearer-токена", configPath())
	}
	if status.PublicURL == "" {
		return status, errors.New("ngrok не вернул публичный URL — проверьте, что ngrok установлен и авторизован (ngrok config add-authtoken)")
	}
	return status, nil
}

func (m *Manager) startNotcode(opts Options) error {
	if strings.TrimSpace(opts.Dir) == "" {
		return errors.New("встроенный NotCode не найден рядом с приложением")
	}
	if _, err := os.Stat(filepath.Join(opts.Dir, "package.json")); err != nil {
		return fmt.Errorf("в %s нет package.json", opts.Dir)
	}

	if err := m.ensureDeps(opts); err != nil {
		return err
	}

	cmd := exec.Command(opts.Runner, "run", "start", "--",
		"--port", fmt.Sprintf("%d", opts.Port), "--host", "127.0.0.1")
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	hideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить «%s run start» в %s: %w", opts.Runner, opts.Dir, err)
	}
	m.mu.Lock()
	m.notcode = cmd
	m.startedAt = time.Now()
	m.mu.Unlock()
	m.appendLog("NotCode запущен (pid %d, порт %d)", cmd.Process.Pid, opts.Port)
	done := make(chan struct{})
	go func() {
		m.pump("notcode", stdout)
		close(done)
	}()
	go func() {
		<-done
		m.reap(cmd, opts.Port)
	}()
	return nil
}

// reap дожидается завершения дочернего notcode и честно сообщает об этом в
// лог, а также сбрасывает состояние — иначе карточка статуса продолжала
// показывать «запущен» с pid уже мёртвого процесса.
func (m *Manager) reap(cmd *exec.Cmd, port int) {
	err := cmd.Wait()
	m.mu.Lock()
	if m.notcode == cmd {
		m.notcode = nil
		m.startedAt = time.Time{}
	}
	m.mu.Unlock()
	if err != nil {
		m.appendLine("app", "error", "NotCode на порту %d завершился: %v — смотрите строки notcode выше", port, err)
		return
	}
	m.appendLine("app", "warn", "NotCode на порту %d завершился", port)
}

// requiredDeps — пакеты, без которых notcode падает на старте с
// «Cannot find package 'elysia'». Проверяем именно их, а не саму папку
// node_modules: пустая или обрезанная папка проходила прежнюю проверку.
var requiredDeps = []string{
	"elysia",
	"zod",
	filepath.Join("@modelcontextprotocol", "sdk"),
}

func hasDeps(dir string) bool {
	for _, name := range requiredDeps {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", name)); err != nil {
			return false
		}
	}
	return true
}

// ensureDeps installs node_modules for the bundled notcode copy on first run.
func (m *Manager) ensureDeps(opts Options) error {
	if hasDeps(opts.Dir) {
		return nil
	}
	m.appendLog("устанавливаю зависимости NotCode (%s install)…", opts.Runner)
	cmd := exec.Command(opts.Runner, "install")
	cmd.Dir = opts.Dir
	cmd.Env = os.Environ()
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			m.appendLine("notcode", "info", "[install] %s", trimmed)
		}
	}
	if err != nil {
		return fmt.Errorf("не удалось установить зависимости NotCode: «%s install» в %s завершился с ошибкой (%w). Нужен интернет один раз либо папка node_modules рядом с package.json", opts.Runner, opts.Dir, err)
	}
	if !hasDeps(opts.Dir) {
		return fmt.Errorf("после «%s install» в %s всё ещё нет пакетов elysia/zod/@modelcontextprotocol/sdk", opts.Runner, opts.Dir)
	}
	m.appendLog("зависимости NotCode установлены")
	return nil
}

func (m *Manager) startNgrok(ctx context.Context, opts Options) error {
	if url, err := ngrokPublicURL(ctx, opts.Port); err == nil && url != "" {
		m.mu.Lock()
		m.public = url
		m.mu.Unlock()
		m.appendLog("ngrok уже работает: %s", url)
		return nil
	}

	args := []string{"http", fmt.Sprintf("127.0.0.1:%d", opts.Port)}
	if strings.TrimSpace(opts.NgrokDomain) != "" {
		args = append(args, "--domain="+strings.TrimSpace(opts.NgrokDomain))
	}
	cmd := exec.Command(opts.NgrokPath, args...)
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить ngrok (%s): %w", opts.NgrokPath, err)
	}
	m.mu.Lock()
	m.ngrok = cmd
	m.mu.Unlock()
	m.appendLog("ngrok запущен (pid %d) → 127.0.0.1:%d", cmd.Process.Pid, opts.Port)
	go m.pump("ngrok", stdout)

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if url, err := ngrokPublicURL(ctx, opts.Port); err == nil && url != "" {
			m.mu.Lock()
			m.public = url
			m.mu.Unlock()
			m.appendLog("публичный URL: %s", url)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
	return errors.New("ngrok не ответил на 127.0.0.1:4040 — проверьте установку и authtoken")
}

// ngrokPublicURL reads the local ngrok agent API.
func ngrokPublicURL(ctx context.Context, port int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:4040/api/tunnels", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
			Config    struct {
				Addr string `json:"addr"`
			} `json:"config"`
		} `json:"tunnels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	suffix := fmt.Sprintf(":%d", port)
	var fallback string
	for _, tunnel := range payload.Tunnels {
		if port > 0 && !strings.Contains(tunnel.Config.Addr, suffix) {
			continue
		}
		if tunnel.Proto == "https" {
			return tunnel.PublicURL, nil
		}
		if fallback == "" {
			fallback = tunnel.PublicURL
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	if port > 0 {
		return "", fmt.Errorf("нет ngrok-туннеля на порт %d", port)
	}
	return "", errors.New("туннели не найдены")
}

func (m *Manager) waitHealth(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = probeHealthErr(ctx, base)
		if lastErr == nil {
			m.appendLog("NotCode готов: %s", base)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(600 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return fmt.Errorf("NotCode не ответил на %s/health: %w", base, lastErr)
	}
	return fmt.Errorf("NotCode не ответил на %s/health", base)
}

func (m *Manager) pump(tag string, reader io.ReadCloser) {
	defer reader.Close()
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(buffer[:n]), "\n"), "\n") {
				trimmed := strings.TrimSpace(stripAnsi(line))
				if trimmed == "" {
					continue
				}
				level := "info"
				lower := strings.ToLower(trimmed)
				switch {
				case strings.Contains(lower, "error") || strings.Contains(trimmed, "❌"):
					level = "error"
				case strings.Contains(lower, "warn") || strings.Contains(trimmed, "⚠"):
					level = "warn"
				}
				m.appendLine(tag, level, "%s", trimmed)
			}
		}
		if err != nil {
			m.appendLine(tag, "warn", "поток вывода закрыт")
			return
		}
	}
}

// stripAnsi убирает цветовые escape-последовательности: в логе они
// выглядели как мусор вида "\u001b[36m".
func stripAnsi(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == 0x1b {
			for i < len(text) && text[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// VerifyTunnel checks that the public ngrok URL really reaches notcode and
// that the Bearer token is accepted.
func VerifyTunnel(ctx context.Context, publicURL, token string) error {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		return errors.New("пустой публичный URL")
	}

	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := probeHealthErr(healthCtx, base); err != nil {
		return fmt.Errorf("туннель %s/health не подтвердил NotCode: %w", base, err)
	}

	statusCtx, cancelStatus := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStatus()
	statusReq, err := http.NewRequestWithContext(statusCtx, http.MethodGet, base+"/status", nil)
	if err != nil {
		return err
	}
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusReq.Header.Set("ngrok-skip-browser-warning", "true")
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		return fmt.Errorf("не удалось проверить токен через %s/status: %w", base, err)
	}
	defer statusResp.Body.Close()
	_, _ = io.Copy(io.Discard, statusResp.Body)
	if statusResp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("NotCode отклонил токен из %s (401)", configPath())
	}
	if statusResp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s/status вернул %d", base, statusResp.StatusCode)
	}

	return verifyMcpEndpoint(ctx, base+McpPath(), token)
}

func verifyMcpEndpoint(ctx context.Context, endpoint, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"neura","version":"1"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(initialize))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("ngrok-skip-browser-warning", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("MCP-маршрут %s недоступен: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
	case http.StatusUnauthorized:
		return fmt.Errorf("NotCode отклонил токен на %s (401)", endpoint)
	case http.StatusNotFound:
		return fmt.Errorf("MCP-маршрут %s вернул 404 — сверьте sse.mcpPath в %s", endpoint, configPath())
	default:
		return fmt.Errorf("%s вернул %d: %s", endpoint, resp.StatusCode, snippet(string(body)))
	}
	if err := checkNgrokBody(body); err != nil {
		return err
	}
	if !strings.Contains(string(body), "protocolVersion") {
		return fmt.Errorf("%s ответил не как MCP-сервер: %s", endpoint, snippet(string(body)))
	}
	return nil
}

// Stop kills both children. Called from the UI and on app shutdown.
func (m *Manager) Stop() {
	m.mu.Lock()
	notcode, ngrok := m.notcode, m.ngrok
	m.notcode, m.ngrok, m.public = nil, nil, ""
	m.startedAt = time.Time{}
	m.mu.Unlock()

	for _, cmd := range []*exec.Cmd{ngrok, notcode} {
		if cmd != nil && cmd.Process != nil {
			_ = kill(cmd)
		}
	}
	m.appendLog("процессы остановлены")
}
