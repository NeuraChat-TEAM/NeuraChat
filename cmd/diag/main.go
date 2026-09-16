// diag — локальная диагностика: дергает те же приватные эндпоинты Notion,
// что и приложение, и печатает сырые ответы. Нужна, чтобы не угадывать
// причины 403 на MCP и ложного «лимит AI» на свежем воркспейсе.
//
//	go run ./cmd/diag                 — usage по активному воркспейсу
//	go run ./cmd/diag -space <id>     — usage по конкретному воркспейсу
//	go run ./cmd/diag -spaces         — список воркспейсов
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"neura/internal/appcfg"
	"neura/internal/notion"
)

func main() {
	spaceFlag := flag.String("space", "", "spaceId (по умолчанию — активный из настроек)")
	listSpaces := flag.Bool("spaces", false, "только список воркспейсов")
	mcpURL := flag.String("mcp", "", "проверить подключение MCP-сервера по этому URL")
	mcpToken := flag.String("token", "", "Bearer-токен MCP-сервера")
	probeAll := flag.Bool("probe-all", false, "проверить все воркспейсы на разрешённые кастомные MCP")
	flag.Parse()

	store, err := appcfg.New()
	if err != nil {
		fail("appcfg: %v", err)
	}
	saved, err := store.LoadSession()
	if err != nil || strings.TrimSpace(saved) == "" {
		fail("нет сохранённой сессии Notion: %v", err)
	}
	cfg, err := notion.ParseCurl(saved)
	if err != nil {
		fail("сессия не разобралась: %v", err)
	}

	client := notion.NewClient()
	client.SetConfig(cfg)

	settings := store.LoadSettings()
	if strings.TrimSpace(settings.ActiveSpaceID) != "" {
		client.UseWorkspace(settings.ActiveUserID, settings.ActiveSpaceID, settings.ActiveSpaceViewID, settings.ActiveSpaceName)
	}

	ctx := context.Background()

	if *listSpaces {
		raw, err := client.PostRaw(ctx, "/api/v3/getSpaces", map[string]interface{}{})
		show("getSpaces", raw, err)
		return
	}

	if *probeAll {
		// Карта «где кастомные MCP разрешены»: одна и та же сессия, один URL,
		// разные воркспейсы.
		accounts, err := client.Accounts(ctx)
		if err != nil {
			fail("Accounts: %v", err)
		}
		auth := []interface{}{}
		if strings.TrimSpace(*mcpToken) != "" {
			auth = append(auth, map[string]interface{}{
				"name":  "Authorization",
				"value": "Bearer " + strings.TrimSpace(*mcpToken),
			})
		}
		url := strings.TrimSpace(*mcpURL)
		for _, account := range accounts {
			fmt.Printf("аккаунт %s (%s)\n", account.Name, account.Email)
			for _, space := range account.Spaces {
				if err := client.UseWorkspace(account.UserID, space.ID, space.SpaceViewID, space.Name); err != nil {
					fmt.Printf("  %-38s %-28s UseWorkspace: %v\n", space.ID, space.Name, err)
					continue
				}
				verdict := "пропущен (нет -mcp)"
				if url != "" {
					_, err := client.PostRaw(ctx, "/api/v3/validateMcpConnection", map[string]interface{}{
						"serverUrl":         url,
						"spaceId":           space.ID,
						"authHeaders":       auth,
						"initiationContext": "connect",
					})
					verdict = "MCP разрешён"
					if err != nil {
						verdict = shortErr(err)
					}
				}
				fmt.Printf("  %-38s %-28s plan=%-12s %s\n", space.ID, space.Name, space.PlanType, verdict)
			}
		}
		return
	}

	spaceID := strings.TrimSpace(*spaceFlag)
	if spaceID == "" {
		spaceID = strings.TrimSpace(settings.ActiveSpaceID)
	}
	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	// Важно: заголовки тоже должны указывать на тестируемый воркспейс.
	if spaceID != cfg.SpaceID {
		if err := client.UseWorkspace("", spaceID, "", ""); err != nil {
			fail("UseWorkspace: %v", err)
		}
	}
	fmt.Println("spaceId       :", spaceID)
	fmt.Println("activeSpaceId :", settings.ActiveSpaceID)
	fmt.Println("curlSpaceId   :", cfg.SpaceID)
	fmt.Println("userId        :", cfg.UserID)
	fmt.Println("spaceViewId   :", cfg.SpaceViewID)
	fmt.Println()
	fmt.Println("=== заголовки сессии (секреты скрыты)")
	for name, value := range cfg.Headers {
		lower := strings.ToLower(name)
		if lower == "cookie" || lower == "authorization" {
			fmt.Printf("  %s = <скрыто, %d симв.>\n", name, len(value))
			continue
		}
		fmt.Printf("  %s = %s\n", name, value)
	}
	fmt.Println()

	if strings.TrimSpace(*mcpURL) != "" {
		// Ровно те же два шага, что в HAR веб-клиента.
		raw, err := client.PostRaw(ctx, "/api/v3/checkMcpOAuthSupport", map[string]interface{}{
			"serverUrl": *mcpURL,
		})
		show("checkMcpOAuthSupport", raw, err)

		auth := []interface{}{}
		if strings.TrimSpace(*mcpToken) != "" {
			auth = append(auth, map[string]interface{}{
				"name":  "Authorization",
				"value": "Bearer " + strings.TrimSpace(*mcpToken),
			})
		}
		raw, err = client.PostRaw(ctx, "/api/v3/validateMcpConnection", map[string]interface{}{
			"serverUrl":         *mcpURL,
			"spaceId":           spaceID,
			"authHeaders":       auth,
			"initiationContext": "connect",
		})
		show("validateMcpConnection", raw, err)
		return
	}

	body := map[string]interface{}{"spaceId": spaceID}
	for _, path := range []string{
		"/api/v3/getCreditRateLimitStatus",
		"/api/v3/getAIUsageEligibilityV2",
	} {
		raw, err := client.PostRaw(ctx, path, body)
		show(path, raw, err)
	}

	usage, err := client.AIUsage(ctx, spaceID)
	if err != nil {
		fmt.Println("AIUsage error:", err)
	}
	pretty, _ := json.MarshalIndent(usage, "", "  ")
	fmt.Println("=== что видит UI (notion.Usage) ===")
	fmt.Println(string(pretty))
}

func show(label string, raw []byte, err error) {
	fmt.Println("===", label)
	if err != nil {
		fmt.Println("ERR:", err)
		fmt.Println()
		return
	}
	var any interface{}
	if json.Unmarshal(raw, &any) == nil {
		pretty, _ := json.MarshalIndent(any, "", "  ")
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(raw))
	}
	fmt.Println()
}

func shortErr(err error) string {
	text := err.Error()
	if strings.Contains(text, "Custom MCP servers are disabled") {
		return "403 кастомные MCP выключены"
	}
	if len(text) > 90 {
		text = text[:90] + "…"
	}
	return text
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
