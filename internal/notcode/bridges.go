package notcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
)

// BridgeSpec — описание локального (stdio) MCP-сервера, который notcode
// поднимает дочерним процессом и отдаёт наружу как обычный HTTP-эндпоинт
// /bridge/<slug>/mcp. Notion умеет подключать только сетевые серверы, поэтому
// npx-пакеты попадают в Notion именно через этот мост.
type BridgeSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug делает из произвольного имени безопасный кусок URL.
func Slug(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "mcp"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

// readBridges достаёт карту мостов из конфига, не трогая остальные ключи.
func readBridges() (map[string]BridgeSpec, error) {
	out := map[string]BridgeSpec{}
	path := configPath()
	if path == "" {
		return out, errors.New("не удалось определить домашнюю папку")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	var doc struct {
		Bridges map[string]BridgeSpec `json:"bridges"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, err
	}
	for slug, spec := range doc.Bridges {
		if strings.TrimSpace(spec.Command) == "" {
			continue
		}
		out[slug] = spec
	}
	return out, nil
}

// Bridges — список настроенных мостов (для UI и диагностики).
func Bridges() (map[string]BridgeSpec, error) { return readBridges() }

// SetBridge добавляет/обновляет мост в ~/.notcode/config.json. Сервер
// перечитывает файл сам, поэтому перезапуск notcode не нужен.
func SetBridge(slug string, spec BridgeSpec) error {
	slug = Slug(slug)
	if strings.TrimSpace(spec.Command) == "" {
		return errors.New("не указана команда запуска MCP-сервера")
	}
	bridges, err := readBridges()
	if err != nil {
		return err
	}
	bridges[slug] = spec
	return writeConfigFields(map[string]interface{}{"bridges": bridges})
}

// RemoveBridge убирает мост из конфига.
func RemoveBridge(slug string) error {
	slug = Slug(slug)
	bridges, err := readBridges()
	if err != nil {
		return err
	}
	if _, ok := bridges[slug]; !ok {
		return nil
	}
	delete(bridges, slug)
	return writeConfigFields(map[string]interface{}{"bridges": bridges})
}

// BridgeURL собирает публичный адрес моста из базового адреса туннеля.
func BridgeURL(publicURL, slug string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if base == "" {
		return ""
	}
	return base + "/bridge/" + Slug(slug) + "/mcp"
}

// VerifyBridge проверяет мост тем же рукопожатием, что и основной MCP-
// эндпоинт: лучше упасть здесь, чем отдать Notion нерабочий адрес.
func VerifyBridge(ctx context.Context, publicURL, slug, token string) error {
	endpoint := BridgeURL(publicURL, slug)
	if endpoint == "" {
		return errors.New("туннель ngrok ещё не поднят")
	}
	return verifyMcpEndpoint(ctx, endpoint, token)
}
