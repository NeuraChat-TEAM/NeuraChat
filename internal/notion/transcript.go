package notion

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Event is what the UI consumes. It is intentionally flat: the renderer only
// appends deltas and never has to understand Notion's patch protocol.
type Event struct {
	Type   string                 `json:"type"`
	Delta  string                 `json:"delta,omitempty"`
	ID     string                 `json:"id,omitempty"`
	Name   string                 `json:"name,omitempty"`
	Server string                 `json:"server,omitempty"`
	Title  string                 `json:"title,omitempty"`
	Args   map[string]interface{} `json:"args,omitempty"`
	Result interface{}            `json:"result,omitempty"`
	Label  string                 `json:"label,omitempty"`
	Status string                 `json:"status,omitempty"`
	// Message — текст ошибки для UI. Раньше ошибки уходили в Delta,
	// а фронтенд читал ev.message — и потому молчал.
	Message string                `json:"message,omitempty"`
	ThreadID string               `json:"threadId,omitempty"`
}

// part is one projected piece of the assistant turn.
type part struct {
	kind   string // text | thinking | tool_use
	id     string
	name   string
	server string
	text   string
	args   map[string]interface{}
	result interface{}
	done   bool
}

// Accumulator applies Notion's `patch` stream onto a transcript snapshot and
// derives incremental UI events by diffing the projection.
//
// Rebuilding the projection on every patch is far more robust than mirroring
// Notion's pointer semantics one-to-one, which is where the old TS code broke
// whenever Notion shuffled its step shapes.
type Accumulator struct {
	steps []interface{}
	prev  []part
}

func NewAccumulator() *Accumulator { return &Accumulator{} }

// Reset installs a fresh snapshot (patch-start / patch-sync).
func (a *Accumulator) Reset(snapshot interface{}) []Event {
	steps, _ := snapshot.([]interface{})
	a.steps = steps
	a.prev = nil
	return a.diff()
}

// Apply consumes the `v` array of a patch frame.
func (a *Accumulator) Apply(ops interface{}) []Event {
	list, ok := ops.([]interface{})
	if !ok {
		return nil
	}
	root := map[string]interface{}{"s": a.steps}
	for _, raw := range list {
		op, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := op["o"].(string)
		pointer, _ := op["p"].(string)
		applyOp(root, splitPointer(pointer), kind, op["v"])
	}
	if steps, ok := root["s"].([]interface{}); ok {
		a.steps = steps
	}
	return a.diff()
}

func splitPointer(pointer string) []string {
	trimmed := strings.Trim(pointer, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// applyOp walks a JSON pointer and mutates the container in place.
// Notion ops: a = append, p = patch/append string, r = replace, x = remove.
func applyOp(container interface{}, path []string, kind string, value interface{}) interface{} {
	if len(path) == 0 {
		return value
	}
	key := path[0]
	rest := path[1:]

	switch node := container.(type) {
	case map[string]interface{}:
		current := node[key]
		if len(rest) > 0 {
			node[key] = applyOp(current, rest, kind, value)
			return node
		}
		node[key] = mutate(current, kind, value)
		if kind == "x" {
			delete(node, key)
		}
		return node

	case []interface{}:
		if key == "-" {
			return append(node, value)
		}
		index, err := strconv.Atoi(key)
		if err != nil {
			return node
		}
		for index >= len(node) {
			node = append(node, map[string]interface{}{})
		}
		if len(rest) > 0 {
			node[index] = applyOp(node[index], rest, kind, value)
			return node
		}
		if kind == "x" {
			return append(node[:index], node[index+1:]...)
		}
		node[index] = mutate(node[index], kind, value)
		return node

	case nil:
		if _, err := strconv.Atoi(key); err == nil || key == "-" {
			return applyOp([]interface{}{}, path, kind, value)
		}
		return applyOp(map[string]interface{}{}, path, kind, value)
	}
	return container
}

func mutate(current interface{}, kind string, value interface{}) interface{} {
	switch kind {
	case "p":
		// Streaming text chunk: append when both sides are strings.
		if chunk, ok := value.(string); ok {
			if existing, ok := current.(string); ok {
				return existing + chunk
			}
			return chunk
		}
		if patch, ok := value.(map[string]interface{}); ok {
			if target, ok := current.(map[string]interface{}); ok {
				for k, v := range patch {
					target[k] = v
				}
				return target
			}
		}
		return value
	case "a":
		if list, ok := current.([]interface{}); ok {
			return append(list, value)
		}
		if current == nil {
			return []interface{}{value}
		}
		return value
	default: // "r" and anything unknown
		return value
	}
}

// project flattens the transcript into the parts the UI renders.
func (a *Accumulator) project() []part {
	var parts []part
	results := map[string]interface{}{}
	toolSteps := map[string]struct{}{}

	for _, raw := range a.steps {
		step, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := step["type"].(string)
		switch kind {
		case "agent-tool-result":
			// Новый формат стрима: инструмент приходит отдельным шагом,
			// в котором есть и вызов (input), и результат.
			id, _ := stringField(step, "toolUseId", "toolCallId", "id")
			if id == "" {
				continue
			}
			if payload, ok := toolResultPayload(step); ok {
				results[id] = payload
			}
			if _, seen := toolSteps[id]; seen {
				continue
			}
			toolSteps[id] = struct{}{}
			name, _ := stringField(step, "toolName", "name", "toolType")
			server, _ := stringField(step, "moduleName", "serverName", "integrationName")
			args, _ := step["input"].(map[string]interface{})
			parts = append(parts, part{kind: "tool_use", id: id, name: name, server: server, args: args})
		case "agent-inference":
			// `value` — это массив частей (text/thinking/tool_use).
			// Раньше здесь ожидался объект с полем "parts", из-за чего текст
			// ответа терялся целиком и чат выглядел пустым.
			list := inferenceParts(step["value"])
			for _, rawPart := range list {
				p, ok := rawPart.(map[string]interface{})
				if !ok {
					continue
				}
				switch t, _ := p["type"].(string); t {
				case "text":
					text, _ := p["content"].(string)
					parts = append(parts, part{kind: "text", text: text})
				case "thinking":
					text, _ := p["content"].(string)
					parts = append(parts, part{kind: "thinking", text: text})
				case "tool_use":
					id, _ := stringField(p, "toolUseId", "id")
					if id != "" {
						if _, seen := toolSteps[id]; seen {
							continue
						}
						toolSteps[id] = struct{}{}
					}
					name, _ := stringField(p, "name", "toolName")
					server, _ := stringField(p, "serverName", "integrationName", "moduleName")
					args, _ := p["input"].(map[string]interface{})
					if args == nil {
						args, _ = p["arguments"].(map[string]interface{})
					}
					parts = append(parts, part{kind: "tool_use", id: id, name: name, server: server, args: args})
				}
			}
		}
	}

	for i := range parts {
		if parts[i].kind != "tool_use" {
			continue
		}
		if result, ok := results[parts[i].id]; ok {
			parts[i].result = result
			parts[i].done = true
		}
	}
	return parts
}

// inferenceParts принимает и массив частей (актуальный формат Notion),
// и объект с полем "parts" (старый формат) — чтобы стрим не ломался
// при очередном изменении схемы на стороне Notion.
func inferenceParts(value interface{}) []interface{} {
	switch typed := value.(type) {
	case []interface{}:
		return typed
	case map[string]interface{}:
		if list, ok := typed["parts"].([]interface{}); ok {
			return list
		}
		if list, ok := typed["value"].([]interface{}); ok {
			return list
		}
	}
	return nil
}

// toolResultPayload достаёт результат инструмента из шага agent-tool-result.
// Пока результата нет, шаг содержит только вызов — тогда возвращаем false,
// чтобы карточка инструмента осталась в состоянии «выполняется».
func toolResultPayload(step map[string]interface{}) (interface{}, bool) {
	for _, key := range []string{"output", "result", "value", "content"} {
		if value, ok := step[key]; ok && value != nil {
			return value, true
		}
	}
	if state, _ := step["state"].(string); state == "error" {
		if text, ok := stringField(step, "error", "errorMessage"); ok {
			return text, true
		}
	}
	return nil, false
}

func stringField(node map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := node[key].(string); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

// diff emits only what changed since the previous projection.
func (a *Accumulator) diff() []Event {
	next := a.project()
	var events []Event

	for i, current := range next {
		var before part
		if i < len(a.prev) {
			before = a.prev[i]
		}

		switch current.kind {
		case "text", "thinking":
			delta := current.text
			if before.kind == current.kind && strings.HasPrefix(current.text, before.text) {
				delta = current.text[len(before.text):]
			}
			if delta == "" {
				continue
			}
			if current.kind == "text" {
				events = append(events, Event{Type: "text-delta", Delta: delta})
			} else {
				events = append(events, Event{Type: "reasoning-delta", Delta: delta})
			}
		case "tool_use":
			if before.kind != "tool_use" || before.id != current.id {
				events = append(events, Event{
					Type: "tool-call", ID: current.id, Name: current.name,
					Server: current.server, Args: current.args,
				})
			} else if !sameJSON(before.args, current.args) {
				events = append(events, Event{
					Type: "tool-args", ID: current.id, Name: current.name,
					Server: current.server, Args: current.args,
				})
			}
			if current.done && !before.done {
				events = append(events, Event{Type: "tool-result", ID: current.id, Name: current.name, Result: current.result})
			}
		}
	}

	a.prev = next
	return events
}

func sameJSON(a, b interface{}) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(left) == string(right)
}
