package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	debugEntryLimit = 30
	// Раньше в отладку летело до 96 KB одной строкой — именно от этого
	// блоки в UI расползались и выходили за рамки. Для диагностики достаточно 16 KB.
	debugPreviewLimit = 16 * 1024
	// Тело запроса в cURL тоже обрезаем: полный transcript никто не читает.
	debugRequestBodyLimit = 4 * 1024
	requestTimeout        = 5 * time.Minute
)

// DebugEntry mirrors one replayed Notion request for the Debug settings tab.
type DebugEntry struct {
	ID          string `json:"id"`
	StartedAt   string `json:"startedAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	RequestCurl string `json:"requestCurl"`
	Status      int    `json:"status,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	Preview     string `json:"responsePreview"`
	Bytes       int64  `json:"responseBytes"`
	Truncated   bool   `json:"responseTruncated"`
	Error       string `json:"error,omitempty"`

	started time.Time
}

// Client replays Notion's private API with the imported browser session.
// net/http negotiates HTTP/2 over TLS via ALPN, so no manual h2 wiring is
// needed (that was the fragile part of the Node sidecar).
type Client struct {
	mu     sync.RWMutex
	cfg    *Config
	http   *http.Client
	debugs []*DebugEntry
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				ForceAttemptHTTP2:   true,
				MaxIdleConns:        16,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 20 * time.Second,
			},
		},
	}
}

func (c *Client) SetConfig(cfg *Config) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

func (c *Client) Config() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Client) Connected() bool { return c.Config() != nil }

func (c *Client) require() (*Config, error) {
	cfg := c.Config()
	if cfg == nil {
		return nil, errors.New("сначала подключите Notion в настройках")
	}
	return cfg, nil
}

// Post sends a JSON body and returns the live response for streaming reads.
func (c *Client) Post(ctx context.Context, path string, body interface{}) (*http.Response, *DebugEntry, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}

	entry := c.beginDebug(cfg, path, payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Origin+path, bytes.NewReader(payload))
	if err != nil {
		c.finishDebug(entry, err)
		return nil, nil, err
	}
	for name, value := range cfg.Headers {
		if blockedHeaders[strings.ToLower(name)] {
			continue
		}
		req.Header.Set(name, value)
	}
	req.Header.Set("content-type", "application/json")

	// runInferenceTranscript — долгоживущий NDJSON-поток. Общий Client.Timeout
	// включает не только подключение, но и ЧТЕНИЕ ВСЕГО body, поэтому ровно через
	// requestTimeout приложение обрывало ответ с «context deadline exceeded»,
	// хотя Notion продолжал генерацию. Для стрима отключаем общий дедлайн;
	// остановка по кнопке и закрытие приложения всё равно отменяют req.Context().
	httpClient := c.http
	if path == inferencePath {
		streamClient := *c.http
		streamClient.Timeout = 0
		httpClient = &streamClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		c.finishDebug(entry, err)
		return nil, nil, err
	}
	entry.Status = resp.StatusCode
	return resp, entry, nil
}

// PostJSON sends a request and decodes the full response body.
func (c *Client) PostJSON(ctx context.Context, path string, body interface{}) (map[string]interface{}, error) {
	raw, err := c.PostRaw(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]interface{}{}, nil
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("Notion вернул некорректный JSON: %w", err)
	}
	return out, nil
}

func (c *Client) PostRaw(ctx context.Context, path string, body interface{}) ([]byte, error) {
	resp, entry, err := c.Post(ctx, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	c.appendDebug(entry, raw)
	if readErr != nil {
		c.finishDebug(entry, readErr)
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := string(raw)
		if len(detail) > 500 {
			detail = detail[:500]
		}
		err := fmt.Errorf("Notion вернул %d: %s", resp.StatusCode, detail)
		c.finishDebug(entry, err)
		return nil, err
	}
	c.finishDebug(entry, nil)
	return raw, nil
}

// ---------------------------------------------------------------- debug log

func redact(name, value string) string {
	switch strings.ToLower(name) {
	case "cookie", "x-notion-active-user-header", "authorization":
		return "<redacted>"
	}
	return value
}

func (c *Client) beginDebug(cfg *Config, path string, payload []byte) *DebugEntry {
	var sb strings.Builder
	sb.WriteString("curl '" + cfg.Origin + path + "'")
	for name, value := range cfg.Headers {
		sb.WriteString(" \\\n  -H '" + name + ": " + redact(name, value) + "'")
	}
	bodyPreview := string(payload)
	if len(bodyPreview) > debugRequestBodyLimit {
		bodyPreview = bodyPreview[:debugRequestBodyLimit] + "…"
	}
	sb.WriteString(" \\\n  --data-raw '" + bodyPreview + "'")

	entry := &DebugEntry{
		ID:          fmt.Sprintf("%d", time.Now().UnixNano()),
		StartedAt:   time.Now().Format(time.RFC3339),
		Method:      http.MethodPost,
		Path:        path,
		RequestCurl: sb.String(),
		started:     time.Now(),
	}

	c.mu.Lock()
	c.debugs = append(c.debugs, entry)
	if len(c.debugs) > debugEntryLimit {
		c.debugs = c.debugs[len(c.debugs)-debugEntryLimit:]
	}
	c.mu.Unlock()
	return entry
}

func (c *Client) appendDebug(entry *DebugEntry, chunk []byte) {
	if entry == nil || len(chunk) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.Bytes += int64(len(chunk))
	if remaining := debugPreviewLimit - len(entry.Preview); remaining > 0 {
		if len(chunk) > remaining {
			entry.Preview += string(chunk[:remaining])
			entry.Truncated = true
		} else {
			entry.Preview += string(chunk)
		}
	} else {
		entry.Truncated = true
	}
}

func (c *Client) finishDebug(entry *DebugEntry, err error) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.FinishedAt != "" {
		return
	}
	entry.FinishedAt = time.Now().Format(time.RFC3339)
	entry.DurationMs = time.Since(entry.started).Milliseconds()
	if err != nil {
		entry.Error = err.Error()
	}
}

func (c *Client) DebugEntries() []DebugEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]DebugEntry, 0, len(c.debugs))
	for i := len(c.debugs) - 1; i >= 0; i-- {
		out = append(out, *c.debugs[i])
	}
	return out
}

func (c *Client) ClearDebug() {
	c.mu.Lock()
	c.debugs = nil
	c.mu.Unlock()
}
