package appcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"neura/internal/dpapi"
	"neura/internal/ngrokbin"
)

const (
	sessionFile        = "notion-session.dpapi"
	ngrokTokenFile     = "ngrok-authtoken.dpapi"
	settingsFile       = "settings.dpapi"
	legacySettingsFile = "settings.json"
	maxSession         = 2 << 20 // 2 MiB, same cap as the Tauri build
)

// McpServer — один MCP-сервер, подключённый через приложение. В Notion
// workflow_module живёт внутри space и не переезжает в новый воркспейс,
// поэтому адрес и токен держим локально и подключаем заново.
type McpServer struct {
	Name      string `json:"name"`
	ServerURL string `json:"serverUrl"`
	Token     string `json:"token,omitempty"`
	AutoWrite bool   `json:"runWriteToolsAutomatically"`

	// Transport: "http" (Streamable HTTP / SSE, адрес открыт наружу)
	// или "stdio" (локальный npm/uvx-пакет, который мы сами запускаем
	// и проксируем наружу через тот же ngrok-туннель).
	Transport string `json:"transport,omitempty"`

	// Command/Args/Env — только для stdio: например npx -y @playwright/mcp@latest.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Headers — произвольные заголовки (X-API-Key и т.п.).
	// Query — параметры строки запроса: Smithery требует ?api_key=...&profile=...
	// и без них отвечает отказом ещё до рукопожатия.
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`

	// AutoRun — разрешить выполнять инструменты без подтверждения (run automatically).
	AutoRun bool `json:"autoRun,omitempty"`
}

// RememberMcpServer добавляет или обновляет запись по имени сервера.
func RememberMcpServer(list []McpServer, in McpServer) []McpServer {
	in.Name = strings.TrimSpace(in.Name)
	in.ServerURL = strings.TrimSpace(in.ServerURL)
	if in.ServerURL == "" {
		return list
	}
	if in.Name == "" {
		in.Name = "mcp"
	}
	for i := range list {
		if strings.EqualFold(list[i].Name, in.Name) || list[i].ServerURL == in.ServerURL {
			list[i] = in
			return list
		}
	}
	return append(list, in)
}

// ForgetMcpServer убирает сервер из списка автоподключения.
func ForgetMcpServer(list []McpServer, name, serverURL string) []McpServer {
	out := make([]McpServer, 0, len(list))
	for _, item := range list {
		if (serverURL != "" && item.ServerURL == serverURL) ||
			(name != "" && strings.EqualFold(item.Name, name)) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// Settings is everything the user can tweak from the Settings dialog.
type Settings struct {
	// Chat
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	SystemPrompt    string `json:"systemPrompt"`
	AutoPrependMcp  bool   `json:"autoPrependMcp"`
	SendWithEnter   bool   `json:"sendWithEnter"`
	// SearchAllSources = «All sources I can access». По умолчанию false.
	SearchAllSources bool `json:"searchAllSources"`

	// ChatMode is the actual wire protocol:
	//   "legacy" — old runInferenceTranscript config;
	//   "v2"     — agent-capable workflow over runInferenceTranscript;
	//   "v3"     — Agent Service create/send/transcript endpoints.
	ChatMode string `json:"chatMode"`
	// Ниже — общие агентные опции; V3 передаёт их напрямую, V2 — через config.
	BrowserEnabled          bool `json:"browserEnabled"`
	SkipApprovals           bool `json:"skipApprovals"`
	UseMemories             bool `json:"useMemories"`
	ExcludeChatFromMemories bool `json:"excludeChatFromMemories"`
	SuggestedEdits          bool `json:"suggestedEdits"`

	// Активный аккаунт и воркспейс (переключатель в сайдбаре).
	ActiveUserID      string `json:"activeUserId"`
	ActiveSpaceID     string `json:"activeSpaceId"`
	ActiveSpaceViewID string `json:"activeSpaceViewId"`
	ActiveSpaceName   string `json:"activeSpaceName"`

	// Appearance
	Theme     string `json:"theme"` // "notion-dark" | "notion-light"
	MotionOff bool   `json:"motionOff"`
	FontScale int    `json:"fontScale"` // percent, 90..120

	// notcode + ngrok
	NotcodeDir    string `json:"notcodeDir"`
	NotcodeCmd    string `json:"notcodeCmd"`    // default: bun
	NotcodePort   int    `json:"notcodePort"`   // default: 3000
	NgrokPath     string `json:"ngrokPath"`     // default: ngrok
	NgrokDomain   string `json:"ngrokDomain"`   // optional reserved domain
	McpServerName     string `json:"mcpServerName"` // name shown in Notion, default notcode
	AutoStart         bool   `json:"autoStartNotcode"`
	RotateNotcodeToken bool   `json:"rotateNotcodeToken"`

	// Last connected MCP module (so we can disconnect / re-use it)
	McpIntegrationID string `json:"mcpIntegrationId"`
	McpServerURL     string `json:"mcpServerUrl"`

	// Все известные MCP-серверы: нужны, чтобы при создании нового
	// воркспейса подключить их туда заново.
	McpServers []McpServer `json:"mcpServers"`

	// Manual overrides for Notion's private MCP endpoints, keyed by step:
	// "metadata", "connect", "create", "list". Empty means auto-detect.
	McpPaths map[string]string `json:"mcpPaths"`
}

func Defaults() Settings {
	return Settings{
		Model:            "",
		ReasoningEffort:  "",
		SystemPrompt:     "",
		AutoPrependMcp:   true,
		SendWithEnter:    true,
		SearchAllSources: false,
		// Beta V2 is the stable agent workflow; it is not the old legacy mode.
		ChatMode:                "v2",
		BrowserEnabled:          true,
		SkipApprovals:           true,
		UseMemories:             true,
		ExcludeChatFromMemories: false,
		SuggestedEdits:          true,
		Theme:                   "notion-dark",
		FontScale:               100,
		NotcodeCmd:              "bun",
		NotcodePort:             3000,
		NgrokPath:               DefaultNgrokPath(),
		NotcodeDir:              DefaultNotcodeDir(),
		McpServerName:           "notcode",
		AutoStart:               true,
		RotateNotcodeToken:      false,
	}
}

// DefaultNgrokPath prefers a downloaded ngrok.exe next to the user's Downloads
// folder and falls back to whatever is on PATH.
func DefaultNgrokPath() string {
	if base, err := os.UserConfigDir(); err == nil {
		if executable, err := ngrokbin.Ensure(filepath.Join(base, "Neura", "tools")); err == nil {
			return executable
		}
	}
	return "ngrok"
}

// EmbeddedNotcodeDir returns the notcode copy that ships with the app:
// <exe dir>/notcode in a packaged build, or ./notcode-embedded in the repo
// while developing. Empty when neither exists.
func EmbeddedNotcodeDir() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "notcode"),
			filepath.Join(exeDir, "notcode-embedded"),
			// `wails dev` runs the binary from a temp/build dir
			filepath.Join(exeDir, "..", "..", "notcode-embedded"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "notcode-embedded"),
			filepath.Join(wd, "notcode"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

// DefaultNotcodeDir prefers the embedded copy and only then looks for an
// unpacked notcode checkout in Downloads.
func DefaultNotcodeDir() string {
	if embedded := EmbeddedNotcodeDir(); embedded != "" {
		return embedded
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	downloads := filepath.Join(home, "Downloads")
	entries, err := os.ReadDir(downloads)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.Contains(strings.ToLower(entry.Name()), "notcode") {
			continue
		}
		root := filepath.Join(downloads, entry.Name())
		for _, candidate := range []string{root, filepath.Join(root, "notcode-main")} {
			if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
				return candidate
			}
		}
	}
	return ""
}

type SessionProfile struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	CaptureMode string `json:"captureMode"`
	Curl        string `json:"curl,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
	Active      bool   `json:"active,omitempty"`
}

type sessionBundle struct {
	Version  int              `json:"version"`
	ActiveID string           `json:"activeId"`
	Profiles []SessionProfile `json:"profiles"`
}

// Store persists settings as plain JSON and the Notion sessions via DPAPI.
type Store struct {
	mu  sync.RWMutex
	dir string
}

func New() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "Neura")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// writeAtomic writes through a temp file so a crash cannot truncate the target.
func (s *Store) writeAtomic(name string, data []byte) error {
	tmp := s.path(name + ".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

func (s *Store) LoadSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := Defaults()
	raw, err := os.ReadFile(s.path(settingsFile))
	if err == nil {
		raw, err = dpapi.Unprotect(raw)
	} else if os.IsNotExist(err) {
		// One-time migration from the old plaintext settings file. It may
		// contain MCP bearer tokens, custom headers/query keys and stdio env.
		raw, err = os.ReadFile(s.path(legacySettingsFile))
	}
	if err != nil {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Defaults()
	}
	switch strings.ToLower(strings.TrimSpace(out.ChatMode)) {
	case "agent", "agent_service", "agent-service", "v3":
		out.ChatMode = "v3"
	case "workflow", "workflow_v2", "workflow-v2", "v2":
		out.ChatMode = "v2"
	case "legacy":
		out.ChatMode = "legacy"
	default:
		out.ChatMode = "v2"
	}
	if out.NotcodeCmd == "" {
		out.NotcodeCmd = "bun"
	}
	if out.NotcodePort == 0 {
		out.NotcodePort = 3000
	}
	if out.NgrokPath == "" {
		out.NgrokPath = DefaultNgrokPath()
	}
	if out.NotcodeDir == "" {
		out.NotcodeDir = DefaultNotcodeDir()
	}
	if out.FontScale < 80 || out.FontScale > 140 {
		out.FontScale = 100
	}
	if out.McpServerName == "" {
		out.McpServerName = "notcode"
	}
	if out.McpPaths == nil {
		out.McpPaths = map[string]string{}
	}
	return out
}

func (s *Store) SaveSettings(v Settings) error {
	switch strings.ToLower(strings.TrimSpace(v.ChatMode)) {
	case "agent", "agent_service", "agent-service", "v3":
		v.ChatMode = "v3"
	case "legacy":
		v.ChatMode = "legacy"
	default:
		v.ChatMode = "v2"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	protected, err := dpapi.Protect(raw)
	if err != nil {
		return err
	}
	if err := s.writeAtomic(settingsFile, protected); err != nil {
		return err
	}
	// Remove the legacy plaintext only after the protected write succeeds.
	if err := os.Remove(s.path(legacySettingsFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) loadSessionBundleLocked() (sessionBundle, error) {
	raw, err := os.ReadFile(s.path(sessionFile))
	if os.IsNotExist(err) {
		return sessionBundle{Version: 1}, nil
	}
	if err != nil {
		return sessionBundle{}, err
	}
	plain, err := dpapi.Unprotect(raw)
	if err != nil {
		return sessionBundle{}, err
	}
	var bundle sessionBundle
	if json.Unmarshal(plain, &bundle) == nil && bundle.Version == 1 {
		return bundle, nil
	}
	// Миграция старого файла, в котором лежала одна cURL-строка.
	curl := strings.TrimSpace(string(plain))
	if curl == "" {
		return sessionBundle{Version: 1}, nil
	}
	return sessionBundle{Version: 1, ActiveID: "legacy", Profiles: []SessionProfile{{
		ID: "legacy", Curl: curl, UpdatedAt: time.Now().UnixMilli(), Active: true,
	}}}, nil
}

func (s *Store) saveSessionBundleLocked(bundle sessionBundle) error {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if len(raw) > maxSession {
		return errors.New("сохранённые сессии слишком большие")
	}
	enc, err := dpapi.Protect(raw)
	if err != nil {
		return err
	}
	return s.writeAtomic(sessionFile, enc)
}

func (s *Store) SaveSessionProfile(id, userID, captureMode, curl string) error {
	curl = strings.TrimSpace(curl)
	if curl == "" || len(curl) > maxSession {
		return errors.New("некорректная или слишком большая сессия")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = strings.TrimSpace(userID)
	}
	if id == "" {
		id = "session-" + fmt.Sprint(time.Now().UnixMilli())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bundle, err := s.loadSessionBundleLocked()
	if err != nil {
		return err
	}
	profile := SessionProfile{ID: id, UserID: userID, CaptureMode: captureMode, Curl: curl, UpdatedAt: time.Now().UnixMilli()}
	found := false
	for i := range bundle.Profiles {
		if bundle.Profiles[i].ID == id {
			bundle.Profiles[i] = profile
			found = true
		}
	}
	if !found {
		bundle.Profiles = append(bundle.Profiles, profile)
	}
	bundle.Version, bundle.ActiveID = 1, id
	return s.saveSessionBundleLocked(bundle)
}

func (s *Store) SaveSession(curl string) error {
	return s.SaveSessionProfile("default", "", "", curl)
}

func (s *Store) LoadSession() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bundle, err := s.loadSessionBundleLocked()
	if err != nil {
		return "", err
	}
	for _, profile := range bundle.Profiles {
		if profile.ID == bundle.ActiveID {
			return profile.Curl, nil
		}
	}
	if len(bundle.Profiles) > 0 {
		return bundle.Profiles[len(bundle.Profiles)-1].Curl, nil
	}
	return "", nil
}

func (s *Store) SessionProfiles() ([]SessionProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bundle, err := s.loadSessionBundleLocked()
	if err != nil {
		return nil, err
	}
	out := make([]SessionProfile, len(bundle.Profiles))
	copy(out, bundle.Profiles)
	for i := range out {
		out[i].Curl = ""
		out[i].Active = out[i].ID == bundle.ActiveID
	}
	return out, nil
}

func (s *Store) ActivateSession(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bundle, err := s.loadSessionBundleLocked()
	if err != nil {
		return "", err
	}
	for _, profile := range bundle.Profiles {
		if profile.ID == id {
			bundle.ActiveID = id
			if err := s.saveSessionBundleLocked(bundle); err != nil {
				return "", err
			}
			return profile.Curl, nil
		}
	}
	return "", errors.New("сессия не найдена")
}

func (s *Store) DeleteSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(sessionFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) SaveNgrokToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("ngrok authtoken пуст")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := dpapi.Protect([]byte(token))
	if err != nil {
		return err
	}
	return s.writeAtomic(ngrokTokenFile, enc)
}

func (s *Store) LoadNgrokToken() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := os.ReadFile(s.path(ngrokTokenFile))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := dpapi.Unprotect(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(plain)), nil
}

func (s *Store) DeleteNgrokToken() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(ngrokTokenFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
