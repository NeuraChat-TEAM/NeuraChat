package notion

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Config is the replayable Notion AI request captured from DevTools.
type Config struct {
	Origin   string                 `json:"origin"`
	Headers  map[string]string      `json:"headers"`
	Template map[string]interface{} `json:"template"`
	SpaceID  string                 `json:"spaceId"`
	UserID   string                 `json:"userId"`
	// SpaceViewID comes from the context step and is required to toggle
	// MCP modules on/off for this space view.
	SpaceViewID string `json:"spaceViewId"`
}

// Headers that must never be replayed: HTTP/2 rejects them or they break framing.
var blockedHeaders = map[string]bool{
	"host": true, "connection": true, "proxy-connection": true,
	"keep-alive": true, "transfer-encoding": true, "upgrade": true,
	"http2-settings": true, "content-length": true, "accept-encoding": true,
}

var urlPattern = regexp.MustCompile(`(?i)^https?://`)

// tokenizeCurl splits a DevTools "Copy as cURL" string (bash or cmd flavour).
func tokenizeCurl(source string) []string {
	input := strings.NewReplacer("^\r\n", " ", "^\n", " ", "\\\r\n", " ", "\\\n", " ").Replace(source)

	var tokens []string
	var token strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}

	for _, char := range input {
		switch {
		case escaped:
			token.WriteRune(char)
			escaped = false
		case (char == '\\' || char == '^') && quote != '\'':
			escaped = true
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				token.WriteRune(char)
			}
		case char == '\'' || char == '"':
			quote = char
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			flush()
		default:
			token.WriteRune(char)
		}
	}
	flush()
	return tokens
}

// ParseCurl turns a runInferenceTranscript cURL command into a replay config.
func ParseCurl(source string) (*Config, error) {
	tokens := tokenizeCurl(strings.TrimSpace(source))
	if len(tokens) == 0 || !strings.EqualFold(tokens[0], "curl") {
		return nil, errors.New("вставьте команду Copy as cURL")
	}

	headers := map[string]string{}
	var target, rawBody, cookie string

	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		var next string
		if i+1 < len(tokens) {
			next = tokens[i+1]
		}
		switch token {
		case "-H", "--header":
			if idx := strings.Index(next, ":"); idx > 0 {
				name := strings.ToLower(strings.TrimSpace(next[:idx]))
				headers[name] = strings.TrimSpace(next[idx+1:])
			}
			i++
		case "-b", "--cookie":
			cookie = next
			i++
		case "-d", "--data", "--data-raw", "--data-binary":
			rawBody = next
			i++
		case "--url":
			target = next
			i++
		default:
			if urlPattern.MatchString(token) {
				target = token
			}
		}
	}

	if cookie != "" && headers["cookie"] == "" {
		headers["cookie"] = cookie
	}
	if target == "" {
		return nil, errors.New("в cURL не найден URL")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("не удалось разобрать URL: %w", err)
	}
	if !strings.HasSuffix(parsed.Path, "/api/v3/runInferenceTranscript") {
		return nil, errors.New("скопируйте как cURL запрос runInferenceTranscript")
	}
	if headers["cookie"] == "" {
		return nil, errors.New("в cURL нет Cookie — включите копирование чувствительных данных в DevTools")
	}
	if rawBody == "" {
		return nil, errors.New("в cURL отсутствует тело запроса")
	}

	var template map[string]interface{}
	if err := json.Unmarshal([]byte(rawBody), &template); err != nil {
		return nil, errors.New("не удалось разобрать JSON-тело cURL")
	}
	spaceID, _ := template["spaceId"].(string)
	transcript, ok := template["transcript"].([]interface{})
	if !ok || spaceID == "" {
		return nil, errors.New("в запросе нет шаблона Notion AI")
	}

	filtered := map[string]string{}
	for name, value := range headers {
		if !blockedHeaders[name] {
			filtered[name] = value
		}
	}
	filtered["content-type"] = "application/json"
	if filtered["accept"] == "" {
		filtered["accept"] = "application/x-ndjson"
	}

	userID := filtered["x-notion-active-user-header"]
	if userID == "" {
		if ctx := lastStepOfType(transcript, "context"); ctx != nil {
			if value, ok := ctx["value"].(map[string]interface{}); ok {
				if id, ok := value["userId"].(string); ok {
					userID = id
					filtered["x-notion-active-user-header"] = id
				}
			}
		}
	}
	if filtered["x-notion-space-id"] == "" {
		filtered["x-notion-space-id"] = spaceID
	}
	if filtered["origin"] == "" {
		filtered["origin"] = parsed.Scheme + "://" + parsed.Host
	}
	// The context step carries the space_view id used by MCP module toggles.
	var spaceViewID string
	for _, raw := range transcript {
		step, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if kind, _ := step["type"].(string); kind != "context" {
			continue
		}
		if value, ok := step["value"].(map[string]interface{}); ok {
			if id, ok := value["spaceViewId"].(string); ok && id != "" {
				spaceViewID = id
			}
		}
	}

	if _, ok := template["threadParentPointer"]; !ok {
		template["threadParentPointer"] = map[string]interface{}{
			"table": "space", "id": spaceID, "spaceId": spaceID,
		}
	}

	return &Config{
		Origin:   parsed.Scheme + "://" + parsed.Host,
		Headers:  filtered,
		Template: template,
		SpaceID:     spaceID,
		UserID:      userID,
		SpaceViewID: spaceViewID,
	}, nil
}

func lastStepOfType(transcript []interface{}, kind string) map[string]interface{} {
	for i := len(transcript) - 1; i >= 0; i-- {
		step, ok := transcript[i].(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := step["type"].(string); t == kind {
			return step
		}
	}
	return nil
}
