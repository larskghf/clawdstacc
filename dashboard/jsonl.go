package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SessionInfo summarises a project's most recent JSONL session: latest activity,
// token totals, model, permission mode, cost, etc.
type SessionInfo struct {
	ModifiedAgo int64  `json:"modified_ago"`
	SizeBytes   int64  `json:"size_bytes"`
	LastEventTS string `json:"last_event_ts,omitempty"`

	LastUserMsg  string `json:"last_user_msg,omitempty"`
	LastToolUse  string `json:"last_tool_use,omitempty"`
	OpenToolUse  string `json:"open_tool_use,omitempty"`
	OpenToolArgs string `json:"open_tool_args,omitempty"`

	Model               string  `json:"model,omitempty"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	EstimatedCost       float64 `json:"estimated_cost"`

	PermissionMode string `json:"permission_mode,omitempty"`
	TurnCount      int    `json:"turn_count"`
}

// claudeProjectDir mirrors Claude Code's path-to-dir mangling: every
// non-[a-zA-Z0-9-] char becomes '-'. /Users/larskghf/_pawfect →
// -Users-larskghf--pawfect
var nonAlnumDash = regexp.MustCompile(`[^a-zA-Z0-9-]`)

func claudeProjectDir(home, projectPath string) string {
	return filepath.Join(home, ".claude", "projects", nonAlnumDash.ReplaceAllString(projectPath, "-"))
}

// metaEventTypes are session bookkeeping events that don't reflect real work.
var metaEventTypes = map[string]bool{
	"agent-name":                 true,
	"custom-title":               true,
	"last-prompt":                true,
	"file-history-snapshot":      true,
	"system_changed":             true,
	"tools_changed":              true,
	"deferred_tools_delta":       true,
	"mcp_instructions_delta":     true,
	"skill_listing":              true,
	"tool_reference":             true,
	"task_reminder":              true,
	"compact_file_reference":     true,
	"previous_message_not_found": true,
	"queue-operation":            true,
	"queued_command":             true,
	"date_change":                true,
	// permission-mode is intentionally NOT meta — we want to capture it.
}

// modelPricing — USD per million tokens. Approximate; adjust if Anthropic
// publishes new rates. Keys are matched as prefixes against `message.model`.
type modelPricing struct {
	Input, Output, CacheRead, CacheWrite float64
}

var pricingTable = map[string]modelPricing{
	"claude-opus":   {Input: 15.00, Output: 75.00, CacheRead: 1.50, CacheWrite: 18.75},
	"claude-sonnet": {Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-haiku":  {Input: 0.80, Output: 4.00, CacheRead: 0.08, CacheWrite: 1.00},
}

func priceFor(model string) modelPricing {
	for prefix, p := range pricingTable {
		if strings.HasPrefix(model, prefix) {
			return p
		}
	}
	return pricingTable["claude-sonnet"] // sane default
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// extractText returns the first text block from a message.content
// (string or list of blocks).
func extractText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		for _, raw := range v {
			b, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := b["type"].(string); t == "text" {
				if s, _ := b["text"].(string); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// toolArgsPreview returns a short, useful preview of the args a tool was
// called with. Falls back to the first string field if the tool isn't known.
func toolArgsPreview(name string, input map[string]any) string {
	if input == nil {
		return ""
	}
	var key string
	switch name {
	case "Bash":
		key = "command"
	case "Edit", "Read", "Write", "MultiEdit", "NotebookEdit":
		key = "file_path"
	case "Glob", "Grep":
		key = "pattern"
	case "WebFetch":
		key = "url"
	case "WebSearch":
		key = "query"
	case "Task", "Agent":
		key = "description"
	case "TaskCreate", "TaskUpdate":
		key = "subject"
	}
	if key != "" {
		if v, ok := input[key].(string); ok && v != "" {
			return truncate(v, 100)
		}
	}
	for _, v := range input {
		if s, ok := v.(string); ok && s != "" {
			return truncate(s, 100)
		}
	}
	return ""
}

// LatestSession reads the most recent JSONL for a project end-to-end and
// extracts a full session summary. Whole-file scan rather than tail-only —
// gives accurate lifetime totals and removes window-artifact bugs in
// "open tool" detection.
func LatestSession(home, projectPath string) *SessionInfo {
	dir := claudeProjectDir(home, projectPath)
	if !isDir(dir) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var jsonls []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		jsonls = append(jsonls, filepath.Join(dir, e.Name()))
	}
	if len(jsonls) == 0 {
		return nil
	}
	sort.Slice(jsonls, func(i, j int) bool {
		ai, _ := os.Stat(jsonls[i])
		aj, _ := os.Stat(jsonls[j])
		return ai.ModTime().After(aj.ModTime())
	})
	latest := jsonls[0]
	stat, err := os.Stat(latest)
	if err != nil {
		return nil
	}

	info := &SessionInfo{
		ModifiedAgo: int64(time.Since(stat.ModTime()).Seconds()),
		SizeBytes:   stat.Size(),
	}

	f, err := os.Open(latest)
	if err != nil {
		return info
	}
	defer f.Close()

	type pendingTool struct {
		name string
		args map[string]any
	}
	pending := map[string]pendingTool{}
	var lastEventKind string
	var lastAssistantToolID string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		etype, _ := ev["type"].(string)

		// Capture permission-mode regardless of meta filter.
		if etype == "permission-mode" {
			if pm, _ := ev["permissionMode"].(string); pm != "" {
				info.PermissionMode = pm
			}
			continue
		}
		if metaEventTypes[etype] {
			continue
		}
		if ts, _ := ev["timestamp"].(string); ts != "" {
			info.LastEventTS = ts
		}

		switch etype {
		case "user":
			msg, _ := ev["message"].(map[string]any)
			text := extractText(msg["content"])
			if text != "" {
				info.LastUserMsg = truncate(text, 140)
				info.TurnCount++
			}
			lastEventKind = "user"
		case "assistant":
			msg, _ := ev["message"].(map[string]any)
			if model, _ := msg["model"].(string); model != "" {
				info.Model = model
			}
			if usage, ok := msg["usage"].(map[string]any); ok {
				info.InputTokens += int64Of(usage["input_tokens"])
				info.OutputTokens += int64Of(usage["output_tokens"])
				info.CacheReadTokens += int64Of(usage["cache_read_input_tokens"])
				info.CacheCreationTokens += int64Of(usage["cache_creation_input_tokens"])
			}
			content, _ := msg["content"].([]any)
			hadToolUse := false
			for _, raw := range content {
				b, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := b["type"].(string); t == "tool_use" {
					hadToolUse = true
					name, _ := b["name"].(string)
					if name == "" {
						name = "?"
					}
					id, _ := b["id"].(string)
					input, _ := b["input"].(map[string]any)
					info.LastToolUse = name
					lastAssistantToolID = id
					if id != "" {
						pending[id] = pendingTool{name: name, args: input}
					}
				}
			}
			if hadToolUse {
				lastEventKind = "assistant_tool"
			} else {
				lastEventKind = "assistant_text"
			}
		case "tool_result":
			id, _ := ev["tool_use_id"].(string)
			if id == "" {
				if msg, ok := ev["message"].(map[string]any); ok {
					id, _ = msg["tool_use_id"].(string)
				}
			}
			if id != "" {
				delete(pending, id)
			}
			lastEventKind = "tool_result"
		}
	}

	if lastEventKind == "assistant_tool" && lastAssistantToolID != "" {
		if t, ok := pending[lastAssistantToolID]; ok {
			info.OpenToolUse = t.name
			info.OpenToolArgs = toolArgsPreview(t.name, t.args)
		}
	}

	// Cost: tokens × per-model price.
	p := priceFor(info.Model)
	info.EstimatedCost =
		float64(info.InputTokens)*p.Input/1e6 +
			float64(info.OutputTokens)*p.Output/1e6 +
			float64(info.CacheReadTokens)*p.CacheRead/1e6 +
			float64(info.CacheCreationTokens)*p.CacheWrite/1e6

	return info
}

func int64Of(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	}
	return 0
}
