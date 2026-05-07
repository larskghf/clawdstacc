package clawd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RemoveProject undoes everything SetupProject created for a project: kills
// the tmux session, stops the launchd agent, deletes the plist, and removes
// our generated .vscode/{tasks,settings}.json files (only if they still carry
// our markers — never overwrites a file the user has rewritten).
//
// What we deliberately do NOT touch:
//   - The project directory itself ($HOME/_<name>)
//   - The Claude conversation history under ~/.claude/projects/...
//   - User-authored .vscode/ files that don't contain our markers
//
// The dashboard's `/api/remove/<name>` endpoint and the `clawdstacc remove`
// CLI subcommand both call this.
func RemoveProject(cfg Config, name string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return fmt.Errorf("invalid project name")
	}

	home, _ := os.UserHomeDir()
	label := "com.user.clawdstacc." + name
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")

	// Sanity check: nothing to do if neither plist nor loaded agent exist.
	plistExists := false
	if _, err := os.Stat(plistPath); err == nil {
		plistExists = true
	}
	if !plistExists && !agentLoaded(label) {
		return fmt.Errorf("nothing to remove for %q (no plist, no loaded agent)", name)
	}

	// 1. Bootout the launchd agent (no-op if not loaded).
	_ = launchctlBootout(label)

	// 2. Delete plist file.
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete plist: %w", err)
	}

	// 3. Kill the tmux session if it's still around.
	_ = exec.Command("tmux", "kill-session", "-t", name).Run()

	// 4. Find the project path so we can clean up generated .vscode files.
	//    If the user already deleted the project directory we just skip this.
	var projectPath string
	for _, p := range ListProjects(cfg) {
		if ProjectName(p) == name {
			projectPath = p
			break
		}
	}
	if projectPath != "" {
		vscodeDir := filepath.Join(projectPath, ".vscode")
		for _, file := range []struct{ name, marker string }{
			{"tasks.json", "claude --rc"},
			{"settings.json", "tmux-claude"},
		} {
			p := filepath.Join(vscodeDir, file.name)
			raw, err := os.ReadFile(p)
			if err == nil && strings.Contains(string(raw), file.marker) {
				_ = os.Remove(p)
			}
		}
		// Best-effort: remove the .vscode dir if it's empty after we cleaned.
		_ = os.Remove(vscodeDir)
	}

	return nil
}
