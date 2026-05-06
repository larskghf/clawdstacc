package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// SetupProject is the dashboard-side equivalent of the per-project block in
// setup.sh: render the plist + the project's .vscode/{tasks,settings}.json,
// then bootstrap the launchd agent. Idempotent — safe to call on a project
// that's already set up.
func SetupProject(cfg Config, projectPath string) error {
	if !isDir(projectPath) {
		return fmt.Errorf("not a directory: %s", projectPath)
	}
	rawName := filepath.Base(projectPath)
	name := strings.TrimPrefix(rawName, "_")
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return fmt.Errorf("invalid project name derived from path: %s", name)
	}

	home, _ := os.UserHomeDir()
	templatesDir := filepath.Join(cfg.RepoDir, "templates")
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgents, 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("mkdir log dir: %w", err)
	}

	common := map[string]string{
		"PROJECT_NAME":       name,
		"PROJECT_PATH":       projectPath,
		"USER_HOME":          home,
		"BREW_PREFIX":        cfg.BrewPrefix,
		"LOG_DIR":            cfg.LogDir,
		"CLAUDE_CONTINUE":    cfg.ClaudeContinue,
		"CLAUDE_EXTRA_FLAGS": cfg.ClaudeExtraFlags,
	}

	// 1. claude.plist
	plistPath := filepath.Join(launchAgents, "com.user.clawdstacc."+name+".plist")
	if err := renderTemplate(filepath.Join(templatesDir, "claude.plist.tmpl"), plistPath, common); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	// 2. .vscode/tasks.json + settings.json (only if missing or recognizably ours)
	vscodeDir := filepath.Join(projectPath, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		return fmt.Errorf("mkdir .vscode: %w", err)
	}
	tasksPath := filepath.Join(vscodeDir, "tasks.json")
	if shouldOverwriteVSCode(tasksPath, "claude --rc") {
		if err := renderTemplate(filepath.Join(templatesDir, "vscode-tasks.json.tmpl"), tasksPath, common); err != nil {
			return fmt.Errorf("render tasks.json: %w", err)
		}
	}
	settingsPath := filepath.Join(vscodeDir, "settings.json")
	if shouldOverwriteVSCode(settingsPath, "tmux-claude") {
		if err := renderTemplate(filepath.Join(templatesDir, "vscode-settings.json.tmpl"), settingsPath, common); err != nil {
			return fmt.Errorf("render settings.json: %w", err)
		}
	}

	// 3. launchd: bootout (no-op if not loaded), bootstrap.
	uid := os.Getuid()
	label := "com.user.clawdstacc." + name
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, label)).Run()
	if out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func renderTemplate(srcPath, dstPath string, vars map[string]string) error {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	content := string(raw)
	for k, v := range vars {
		content = strings.ReplaceAll(content, "__"+k+"__", v)
	}
	return os.WriteFile(dstPath, []byte(content), 0o644)
}

// shouldOverwriteVSCode mirrors the `[ ! -f X ] || grep -q <marker> X` check
// in setup.sh: overwrite if missing, or if the file contains our marker
// (= we wrote it before). Otherwise leave a user-authored file alone.
func shouldOverwriteVSCode(path, marker string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return strings.Contains(string(raw), marker)
}
