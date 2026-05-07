package clawd

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ProjectStatus struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	TmuxAlive   bool         `json:"tmux_alive"`
	TmuxIdle    *int64       `json:"tmux_idle,omitempty"`
	ClaudeAlive bool         `json:"claude_alive"`
	AgentLoaded bool         `json:"agent_loaded"`
	Session     *SessionInfo `json:"session,omitempty"`
}

type StatusSnapshot struct {
	Projects         []ProjectStatus `json:"projects"`
	CodeServerLoaded bool            `json:"code_server_loaded"`
	CodeServerBind   string          `json:"code_server_bind"`
	Timestamp        int64           `json:"ts"`
}

func tmuxSessionInfo(name string) (alive bool, pid string, idle *int64) {
	out := sh(fmt.Sprintf(
		`%s display-message -p -t %s '#{session_activity}|#{pane_pid}'`,
		tmuxShPrefix(), shellQuote(name),
	), "")
	if out == "" || !strings.Contains(out, "|") {
		return false, "", nil
	}
	parts := strings.SplitN(out, "|", 2)
	activityStr, p := parts[0], parts[1]
	// tmux returns "|" with both fields empty (and exit 0!) when -t targets a
	// non-existent session — treat that as not-alive rather than silently
	// reporting a phantom session.
	if activityStr == "" || p == "" {
		return false, "", nil
	}
	if activityTs, err := strconv.ParseInt(activityStr, 10, 64); err == nil {
		ago := time.Now().Unix() - activityTs
		idle = &ago
	}
	return true, p, idle
}

func claudeRunningInPane(panePID string) bool {
	if panePID == "" {
		return false
	}
	pids := []string{panePID}
	for _, child := range strings.Split(sh(fmt.Sprintf("pgrep -P %s", shellQuote(panePID)), ""), "\n") {
		child = strings.TrimSpace(child)
		if child != "" {
			pids = append(pids, child)
		}
	}
	for _, pid := range pids {
		cmd := sh(fmt.Sprintf("ps -p %s -o command=", shellQuote(pid)), "")
		if strings.Contains(strings.ToLower(cmd), "claude") {
			return true
		}
	}
	return false
}

var labelEscapeRE = regexp.MustCompile(`[.*+?()|\[\]{}^$\\]`)

func agentLoaded(label string) bool {
	escaped := labelEscapeRE.ReplaceAllStringFunc(label, func(m string) string { return `\` + m })
	out := sh(fmt.Sprintf(`launchctl list | grep -E '\s%s$'`, escaped), "")
	return out != ""
}

func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

func CollectStatus(cfg Config) StatusSnapshot {
	home, _ := os.UserHomeDir()
	var rows []ProjectStatus
	for _, p := range ListProjects(cfg) {
		name := ProjectName(p)
		alive, pid, idle := tmuxSessionInfo(name)
		rows = append(rows, ProjectStatus{
			Name:        name,
			Path:        p,
			TmuxAlive:   alive,
			TmuxIdle:    idle,
			ClaudeAlive: claudeRunningInPane(pid),
			AgentLoaded: agentLoaded("com.user.clawdstacc." + name),
			Session:     LatestSession(home, p),
		})
	}
	// Sort by recency: smallest "active … ago" first. Projects without a
	// session sink to the bottom (alphabetical among themselves).
	sort.SliceStable(rows, func(i, j int) bool {
		return activitySortKey(rows[i]) < activitySortKey(rows[j])
	})
	return StatusSnapshot{
		Projects:         rows,
		CodeServerLoaded: agentLoaded("com.user.clawdstacc.codeserver"),
		CodeServerBind:   cfg.CodeServerBind,
		Timestamp:        time.Now().Unix(),
	}
}

// activitySortKey returns the "ago" seconds we sort by. Recently-active first.
// Sessions without a JSONL fall to the bottom — we use +Inf so they never
// outrank a real session, regardless of how stale the real session is.
func activitySortKey(p ProjectStatus) float64 {
	if p.Session != nil {
		return float64(p.Session.ModifiedAgo)
	}
	if p.TmuxIdle != nil {
		// No JSONL but tmux session is running → second tier, sort by tmux idle.
		return math.MaxInt32 + float64(*p.TmuxIdle)
	}
	return math.Inf(1)
}

// RestartProject kicks the launchd agent for a project after killing its tmux
// session. Verifies the agent exists (= is loaded into launchd) before acting,
// so unknown names produce a clean 4xx instead of a silent no-op.
func RestartProject(name string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return fmt.Errorf("invalid project name")
	}
	label := "com.user.clawdstacc." + name
	if !agentLoaded(label) {
		return fmt.Errorf("unknown project: %s", name)
	}
	sh(fmt.Sprintf("%s kill-session -t %s 2>/dev/null", tmuxShPrefix(), shellQuote(name)), "")
	uid := os.Getuid()
	cmd := exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, label))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl: %s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
