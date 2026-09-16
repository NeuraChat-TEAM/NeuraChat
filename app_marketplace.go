package main

// Маркетплейс MCP-серверов — живой каталог из официального реестра
// Model Context Protocol (registry.modelcontextprotocol.io). Там тысячи
// серверов с описаниями и адресами remote-эндпоинтов — именно их мы
// показываем и подключаем одной кнопкой через McpConnect.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const registryBase = "https://registry.modelcontextprotocol.io/v0/servers"

// MarketplaceServer — одна карточка в маркетплейсе.
type MarketplaceServer struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Website     string `json:"website,omitempty"`
	// Адрес remote-сервера (streamable-http или sse). Пустой — только локальный запуск.
	ServerURL string `json:"serverUrl,omitempty"`
	Transport string `json:"transport,omitempty"`
	// Для локальных пакетов — как их ставить (npx/uvx и т.д.).
	Install string `json:"install,omitempty"`
}

// MarketplacePage — страница каталога с курсором для «показать ещё».
type MarketplacePage struct {
	Servers    []MarketplaceServer `json:"servers"`
	NextCursor string              `json:"nextCursor,omitempty"`
	Total      int                 `json:"total"`
	Source     string              `json:"source"`
}

var marketplaceHTTP = &http.Client{Timeout: 25 * time.Second}

// Кэш на время сеанса: реестр не любит частых повторных запросов.
var (
	marketMu    sync.Mutex
	marketCache = map[string]MarketplacePage{}
)

// MarketplaceList отдаёт страницу каталога. query — поиск по имени/описанию,
// cursor — курсор из предыдущего ответа, remoteOnly — только те, что можно
// подключить по URL (именно такие умеет Notion).
func (a *App) MarketplaceList(query, cursor string, remoteOnly bool) (MarketplacePage, error) {
	key := fmt.Sprintf("%s|%s|%v", strings.ToLower(strings.TrimSpace(query)), cursor, remoteOnly)
	marketMu.Lock()
	if page, ok := marketCache[key]; ok {
		marketMu.Unlock()
		return page, nil
	}
	marketMu.Unlock()

	page, err := fetchRegistry(a.ctx, query, cursor, remoteOnly)
	if err != nil {
		return page, err
	}

	marketMu.Lock()
	marketCache[key] = page
	marketMu.Unlock()
	return page, nil
}

func fetchRegistry(ctx context.Context, query, cursor string, remoteOnly bool) (MarketplacePage, error) {
	out := MarketplacePage{Source: "registry.modelcontextprotocol.io"}

	params := url.Values{}
	params.Set("limit", "100")
	if q := strings.TrimSpace(query); q != "" {
		params.Set("search", q)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryBase+"?"+params.Encode(), nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", "Neura/1.0 (+mcp marketplace)")

	resp, err := marketplaceHTTP.Do(req)
	if err != nil {
		return out, fmt.Errorf("каталог MCP недоступен: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return out, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("каталог MCP вернул %d", resp.StatusCode)
	}

	var body struct {
		Servers []struct {
			Server struct {
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Version     string `json:"version"`
				WebsiteURL  string `json:"websiteUrl"`
				Remotes     []struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"remotes"`
				Packages []struct {
					RegistryType string `json:"registryType"`
					Identifier   string `json:"identifier"`
					Version      string `json:"version"`
				} `json:"packages"`
			} `json:"server"`
			// Старые ответы реестра кладут поля плоско — поддерживаем оба вида.
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"servers"`
		Metadata struct {
			NextCursor string `json:"nextCursor"`
			Count      int    `json:"count"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return out, fmt.Errorf("не разобрал ответ каталога: %w", err)
	}

	for _, item := range body.Servers {
		srv := item.Server
		name := srv.Name
		if name == "" {
			name = item.Name
		}
		description := srv.Description
		if description == "" {
			description = item.Description
		}
		card := MarketplaceServer{
			Name:        name,
			Title:       srv.Title,
			Description: description,
			Version:     srv.Version,
			Website:     srv.WebsiteURL,
		}
		for _, remote := range srv.Remotes {
			if remote.URL == "" {
				continue
			}
			// streamable-http предпочтительнее sse.
			if card.ServerURL == "" || strings.Contains(remote.Type, "http") {
				card.ServerURL = remote.URL
				card.Transport = remote.Type
			}
		}
		if len(srv.Packages) > 0 {
			pkg := srv.Packages[0]
			switch pkg.RegistryType {
			case "npm":
				card.Install = "npx -y " + pkg.Identifier
			case "pypi":
				card.Install = "uvx " + pkg.Identifier
			default:
				card.Install = pkg.RegistryType + ": " + pkg.Identifier
			}
		}
		if remoteOnly && card.ServerURL == "" {
			continue
		}
		if name == "" {
			continue
		}
		out.Servers = append(out.Servers, card)
	}

	// Сначала те, что подключаются одной кнопкой.
	sort.SliceStable(out.Servers, func(i, j int) bool {
		left, right := out.Servers[i], out.Servers[j]
		if (left.ServerURL != "") != (right.ServerURL != "") {
			return left.ServerURL != ""
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})

	out.NextCursor = body.Metadata.NextCursor
	out.Total = len(out.Servers)
	return out, nil
}
