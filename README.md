# Neura (Go + Wails)

Полный переписывание `neural-chat-tauri` (Tauri + Rust + Node-sidecar) на **Go 1.23 + Wails v2 + React 19 (Vite 6 / SWC)**
с дизайн-системой Notion AI и встроенной интеграцией notcode MCP.

## Что изменилось по сравнению с Tauri-версией

| Было | Стало |
| --- | --- |
| Rust-оболочка + Node.js sidecar (HTTP на localhost, токен, watchdog) | Всё внутри одного Go-процесса, sidecar удалён |
| `tauri_plugin_sql` + миграции | `modernc.org/sqlite` (без CGO), WAL, `internal/store` |
| NDJSON по HTTP между UI и sidecar | Wails-события `chat:event` напрямую в UI |
| Спул ответов в `%TEMP%` плайнтекстом | Только кольцевой буфер в памяти (30 записей, редактирование секретов) |
| `clientVersion` зашит в код | `notion-client-version` и `x-notion-cell-hint*` берутся из импортированного cURL и повторяются 1:1 |
| Неограниченные map состояний | TTL 12 ч + периодическая очистка |
| CORS `*` на локальном порту | Локального HTTP-сервера больше нет |

## Структура

```
main.go              окно Wails (frameless, 1180x820), embed frontend/dist
app.go               сессия, настройки, окно, отладка
app_chat.go          треды, отправка, стрим chat:event, stop
app_mcp.go           notcode + MCP-методы
internal/notion/     curl-парсер, http-клиент, транскрипт, inference, mcp
internal/notcode/    запуск notcode + ngrok, чтение токена
internal/store/      SQLite (треды/сообщения/kv)
internal/appcfg/     настройки + DPAPI-хранилище сессии
frontend/src/        React 19 UI в стиле Notion
```

## Запуск

```bash
go mod tidy
cd frontend && npm install && cd ..
wails dev      # разработка (генерирует биндинги)
wails build    # релиз Neura.exe
```

Требуется: Go ≥ 1.23, Wails CLI v2, Node ≥ 20, для MCP — `bun` и `ngrok` в PATH.

## Подключение к Notion

Настройки → **Подключение**: вставить `Copy as cURL` любого запроса `runInferenceTranscript`.
Из него берутся cookie, `notion-client-version`, `x-notion-space-id`, cell-hint и активный user.
Сессия шифруется DPAPI в `%AppData%/Neura/notion-session.dpapi`.

## notcode MCP в один клик

Настройки → **MCP** → «Подключить notcode MCP» делает:

1. читает `~/.notcode/config.json` (токен, порт, режим);
2. если `GET /health` молчит — запускает `bun run start` в папке notcode и ждёт до 40 с;
3. запускает `ngrok http <порт>` и берёт публичный URL из `127.0.0.1:4040/api/tunnels`;
4. проводит тот же флоу, что и веб-Notion: проверка сервера → список инструментов →
   создание `workflow_module` с `Authorization: Bearer <токен notcode>` → `saveTransactionsFanout`
   (`agentPersistenceHelpers.addAgentChatModule` в `space_view.settings.agent_chat_modules`);
5. прописывает системный промпт `use mcp <имя>`.

Удаление и ручное добавление любого MCP (имя + URL + Bearer) есть в той же вкладке.

> Важно: пути MCP-эндпоинтов в `internal/notion/mcp.go` вынесены в константы. В переданных cURL
> имена путей были скрыты, поэтому их нужно сверить с DevTools и при несовпадении поправить в одном блоке
> констант вверху файла. Всё остальное (тела запросов, заголовки, порядок вызовов) совпадает с захватом.

## Настройки (вкладки)

Подключение · MCP · Системный промпт · Модель · Вид · Отладка.
