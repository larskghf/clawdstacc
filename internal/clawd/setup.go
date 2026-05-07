package clawd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SetupProject is the dashboard-side per-project setup: render the plist +
// the project's .vscode/{tasks,settings}.json (when code-server is enabled),
// then bootstrap the launchd agent. Idempotent — safe to call repeatedly.
//
// Used by the dashboard's `/api/setup/<name>` endpoint to onboard a freshly-
// detected project without restarting everything else. The CLI `setup`
// subcommand (cmd_setup.go) handles the global rendering + reload sweep.
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
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgents, 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("mkdir log dir: %w", err)
	}

	common := projectVars(cfg, projectPath, name, home)

	// 1. claude.plist
	plistPath := filepath.Join(launchAgents, "com.user.clawdstacc."+name+".plist")
	if err := writePlist("claude.plist.tmpl", plistPath, common); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	// 2. .vscode/{tasks,settings}.json — only when code-server is enabled.
	//    Skip silently in headless mode; the user has no IDE to configure.
	if cfg.EnableCodeServer {
		vscodeDir := filepath.Join(projectPath, ".vscode")
		if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
			return fmt.Errorf("mkdir .vscode: %w", err)
		}
		tasksPath := filepath.Join(vscodeDir, "tasks.json")
		if shouldOverwriteVSCode(tasksPath, "claude --rc") {
			if err := writePlist("vscode-tasks.json.tmpl", tasksPath, map[string]string{"PROJECT_NAME": name}); err != nil {
				return fmt.Errorf("render tasks.json: %w", err)
			}
		}
		settingsPath := filepath.Join(vscodeDir, "settings.json")
		if shouldOverwriteVSCode(settingsPath, "tmux-claude") {
			if err := writePlist("vscode-settings.json.tmpl", settingsPath, map[string]string{"PROJECT_NAME": name}); err != nil {
				return fmt.Errorf("render settings.json: %w", err)
			}
		}
	}

	// 3. launchd: bootout (no-op if not loaded), bootstrap.
	_ = launchctlBootout(plistPath)
	if err := launchctlBootstrap(plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	return nil
}

// shouldOverwriteVSCode mirrors the bash `[ ! -f X ] || grep -q <marker> X`
// check: write if missing, or if the file contains our marker (= we wrote it
// before). Otherwise leave a user-authored file alone.
func shouldOverwriteVSCode(path, marker string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return strings.Contains(string(raw), marker)
}
