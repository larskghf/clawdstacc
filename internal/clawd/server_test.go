package clawd

import "testing"

func TestModelShort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"claude-opus-4-7", "Opus 4.7"},
		{"claude-sonnet-4-6", "Sonnet 4.6"},
		{"claude-haiku-4-5", "Haiku 4.5"},
		{"claude-opus", "Opus"},
		{"claude-opus-4", "Opus 4"},
		{"claude-opus-4-7-20251001", "Opus 4.7"}, // extra suffix is fine, we only use the first three
		{"", ""},
		{"claude-", ""},
	}
	for _, c := range cases {
		if got := modelShort(c.in); got != c.want {
			t.Errorf("modelShort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTokenFmt(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{12345, "12.3k"},
		{999_999, "1000.0k"},
		{1_000_000, "1.0M"},
		{1_500_000, "1.5M"},
		{1_000_000_000, "1.0B"},
	}
	for _, c := range cases {
		if got := tokenFmt(c.in); got != c.want {
			t.Errorf("tokenFmt(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCostFmt(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.001, "$0.00"}, // sub-cent rounds to zero display
		{0.42, "$0.42"},
		{1.005, "$1.00"}, // banker's rounding via Sprintf %.2f
		{99.99, "$99.99"},
		{1234.5, "$1234.50"},
	}
	for _, c := range cases {
		if got := costFmt(c.in); got != c.want {
			t.Errorf("costFmt(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSummary(t *testing.T) {
	idle := int64(120)
	snap := StatusSnapshot{
		Projects: []ProjectStatus{
			// Ready: tmux + claude alive
			{Name: "a", TmuxAlive: true, ClaudeAlive: true, AgentLoaded: true},
			// Working: ready AND a tool is running
			{Name: "b", TmuxAlive: true, ClaudeAlive: true, AgentLoaded: true,
				Session: &SessionInfo{OpenToolUse: "Bash"}},
			// Idle: tmux up, claude not
			{Name: "c", TmuxAlive: true, ClaudeAlive: false, AgentLoaded: true,
				TmuxIdle: &idle},
			// Down: tmux dead
			{Name: "d", TmuxAlive: false, ClaudeAlive: false, AgentLoaded: true},
			// Needs setup: dir scanned but no agent
			{Name: "e", TmuxAlive: false, ClaudeAlive: false, AgentLoaded: false},
		},
	}
	s := summary(snap)
	if s.Total != 5 {
		t.Errorf("Total = %d, want 5", s.Total)
	}
	if s.Ready != 2 {
		t.Errorf("Ready = %d, want 2", s.Ready)
	}
	if s.Running != 1 {
		t.Errorf("Running = %d, want 1", s.Running)
	}
	if s.Idle != 1 {
		t.Errorf("Idle = %d, want 1", s.Idle)
	}
	if s.Down != 2 {
		t.Errorf("Down = %d, want 2", s.Down)
	}
	if s.Setup != 1 {
		t.Errorf("Setup = %d, want 1", s.Setup)
	}
}
