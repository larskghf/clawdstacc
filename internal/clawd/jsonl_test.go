package clawd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeProjectDir(t *testing.T) {
	cases := []struct {
		home, project, want string
	}{
		{"/Users/x", "/Users/x/_demo", "/Users/x/.claude/projects/-Users-x--demo"},
		{"/Users/x", "/Users/x/work/foo", "/Users/x/.claude/projects/-Users-x-work-foo"},
		{"/Users/x", "/Users/x/_a/.claude/worktrees/silly-lamport", "/Users/x/.claude/projects/-Users-x--a--claude-worktrees-silly-lamport"},
	}
	for _, c := range cases {
		got := claudeProjectDir(c.home, c.project)
		if got != c.want {
			t.Errorf("claudeProjectDir(%q,%q) = %q, want %q", c.home, c.project, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"  hello   world  ", 20, "hello world"}, // collapses whitespace runs
		{"abcdefg", 5, "abcd…"},
		{"line1\nline2", 20, "line1 line2"}, // newlines treated as whitespace
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestExtractText(t *testing.T) {
	if got := extractText("plain string"); got != "plain string" {
		t.Errorf("string content = %q", got)
	}
	blocks := []any{
		map[string]any{"type": "tool_result", "content": "ignored"},
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "text", "text": "world"},
	}
	if got := extractText(blocks); got != "hello" {
		t.Errorf("blocks → first text = %q, want %q", got, "hello")
	}
	if got := extractText(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}
}

func TestToolArgsPreview(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "npm test", "description": "run tests"}, "npm test"},
		{"Edit", map[string]any{"file_path": "/tmp/foo.go", "old_string": "x"}, "/tmp/foo.go"},
		{"Read", map[string]any{"file_path": "/etc/hosts"}, "/etc/hosts"},
		{"Glob", map[string]any{"pattern": "**/*.go"}, "**/*.go"},
		{"WebFetch", map[string]any{"url": "https://example.com", "prompt": "x"}, "https://example.com"},
		// unknown tool → fallback to first string field
		{"NewTool", map[string]any{"foo": "bar"}, "bar"},
		// empty input
		{"Bash", nil, ""},
	}
	for _, c := range cases {
		if got := toolArgsPreview(c.name, c.input); got != c.want {
			t.Errorf("toolArgsPreview(%q,%v) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

func TestPriceFor(t *testing.T) {
	if priceFor("claude-opus-4-7").Input != 15.0 {
		t.Errorf("opus input price wrong")
	}
	if priceFor("claude-sonnet-4-6").Input != 3.0 {
		t.Errorf("sonnet input price wrong")
	}
	if priceFor("claude-haiku-4-5").Output != 4.0 {
		t.Errorf("haiku output price wrong")
	}
	// Unknown model falls back to sonnet pricing
	if priceFor("totally-unknown").Input != 3.0 {
		t.Errorf("unknown model fallback should equal sonnet input price")
	}
}

// TestLatestSession_FixtureJSONL writes a representative JSONL stream for a
// project, calls LatestSession against a fake $HOME, and asserts the parsed
// summary matches the events.
func TestLatestSession_FixtureJSONL(t *testing.T) {
	tmpHome := t.TempDir()
	projectPath := filepath.Join(tmpHome, "_demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionDir := claudeProjectDir(tmpHome, projectPath)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(sessionDir, "abc.jsonl")

	// Build a small but representative stream:
	//   user → assistant(tool_use Bash:"ls") → tool_result(matches) → user → assistant(tool_use Edit:"foo.go", no result yet)
	// Plus a permission-mode meta event and an old user message before that.
	events := []map[string]any{
		// older user message (gets overwritten by the latest)
		{"type": "user", "timestamp": "2026-05-06T10:00:00Z",
			"message": map[string]any{"role": "user", "content": "hi"}},
		// assistant with usage + tool_use that completes
		{"type": "assistant", "timestamp": "2026-05-06T10:00:01Z",
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-4-7",
				"usage": map[string]any{
					"input_tokens":                100.0,
					"output_tokens":               50.0,
					"cache_read_input_tokens":     1000.0,
					"cache_creation_input_tokens": 500.0,
				},
				"content": []any{
					map[string]any{"type": "tool_use", "id": "tool1", "name": "Bash",
						"input": map[string]any{"command": "ls"}},
				},
			},
		},
		{"type": "tool_result", "tool_use_id": "tool1"},
		// permission-mode (must be captured even though it's normally meta-ish)
		{"type": "permission-mode", "permissionMode": "bypassPermissions"},
		// latest user message
		{"type": "user", "timestamp": "2026-05-06T10:00:02Z",
			"message": map[string]any{"role": "user", "content": "edit foo"}},
		// assistant with tool_use that has NO matching result → "open"
		{"type": "assistant", "timestamp": "2026-05-06T10:00:03Z",
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-4-7",
				"usage": map[string]any{"input_tokens": 10.0, "output_tokens": 5.0},
				"content": []any{
					map[string]any{"type": "tool_use", "id": "tool2", "name": "Edit",
						"input": map[string]any{"file_path": "/tmp/foo.go"}},
				},
			},
		},
		// trailing meta — must NOT count as the last "kind"
		{"type": "agent-name", "agentName": "demo"},
	}
	var sb strings.Builder
	for _, e := range events {
		b, _ := json.Marshal(e)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(jsonlPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	info := LatestSession(tmpHome, projectPath)
	if info == nil {
		t.Fatal("LatestSession returned nil")
	}
	if info.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q", info.Model)
	}
	if info.LastUserMsg != "edit foo" {
		t.Errorf("LastUserMsg = %q, want %q", info.LastUserMsg, "edit foo")
	}
	if info.LastToolUse != "Edit" {
		t.Errorf("LastToolUse = %q, want %q", info.LastToolUse, "Edit")
	}
	if info.OpenToolUse != "Edit" {
		t.Errorf("OpenToolUse = %q, want %q (last assistant tool_use is unmatched)", info.OpenToolUse, "Edit")
	}
	if info.OpenToolArgs != "/tmp/foo.go" {
		t.Errorf("OpenToolArgs = %q, want %q", info.OpenToolArgs, "/tmp/foo.go")
	}
	if info.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q", info.PermissionMode)
	}
	if info.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", info.TurnCount)
	}
	if info.InputTokens != 110 {
		t.Errorf("InputTokens = %d, want 110", info.InputTokens)
	}
	if info.OutputTokens != 55 {
		t.Errorf("OutputTokens = %d, want 55", info.OutputTokens)
	}
	if info.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens = %d, want 1000", info.CacheReadTokens)
	}
	if info.CacheCreationTokens != 500 {
		t.Errorf("CacheCreationTokens = %d, want 500", info.CacheCreationTokens)
	}
	if info.EstimatedCost <= 0 {
		t.Errorf("EstimatedCost = %v, want positive", info.EstimatedCost)
	}
}

func TestLatestSession_NoSessionDir(t *testing.T) {
	info := LatestSession(t.TempDir(), "/nonexistent/_x")
	if info != nil {
		t.Errorf("expected nil for missing session dir, got %+v", info)
	}
}

// TestLatestSession_OpenToolNotFalsePositive guards the regression where any
// unmatched tool_use anywhere in the file flagged the session as "running"
// even when the LAST event is something else (e.g. an assistant text reply).
func TestLatestSession_OpenToolNotFalsePositive(t *testing.T) {
	tmpHome := t.TempDir()
	projectPath := filepath.Join(tmpHome, "_demo")
	sessionDir := claudeProjectDir(tmpHome, projectPath)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		// tool_use without matching result (simulating a tool_result that
		// rolled out of the read window in the real world)
		{"type": "assistant", "timestamp": "2026-05-06T10:00:00Z",
			"message": map[string]any{
				"role": "assistant", "model": "claude-sonnet-4-6",
				"content": []any{
					map[string]any{"type": "tool_use", "id": "stale", "name": "Bash",
						"input": map[string]any{"command": "ls"}},
				},
			},
		},
		// LAST meaningful event is plain assistant text — so OpenToolUse must be empty.
		{"type": "assistant", "timestamp": "2026-05-06T10:01:00Z",
			"message": map[string]any{
				"role": "assistant", "model": "claude-sonnet-4-6",
				"content": []any{map[string]any{"type": "text", "text": "done"}},
			},
		},
	}
	var sb strings.Builder
	for _, e := range events {
		b, _ := json.Marshal(e)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "abc.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	info := LatestSession(tmpHome, projectPath)
	if info == nil {
		t.Fatal("nil")
	}
	if info.OpenToolUse != "" {
		t.Errorf("OpenToolUse = %q, want empty (last event is assistant_text)", info.OpenToolUse)
	}
	if info.LastToolUse != "Bash" {
		t.Errorf("LastToolUse should still be 'Bash', got %q", info.LastToolUse)
	}
}
