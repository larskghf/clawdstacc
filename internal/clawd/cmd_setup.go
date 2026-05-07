package clawd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cmdSetup runs the equivalent of the old setup.sh: pre-flight checks, render
// every plist, write per-project .vscode/ files (if code-server is enabled),
// and bootstrap launchd. Idempotent.
func cmdSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	confPath := fs.String("conf", defaultConfPath(), "path to clawdstacc.conf")
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}

	// Absolute-ify the conf path before doing anything else: it ends up
	// embedded in the rendered dashboard plist as $CLAWDSTACC_CONF, and
	// launchd starts services in its own working directory — so a relative
	// path silently fails to load there and the dashboard falls back to
	// defaults (CODESERVER_PUBLIC_URL drops out, etc.).
	if abs, err := filepath.Abs(*confPath); err == nil {
		*confPath = abs
	}

	cfg, err := LoadConfig(*confPath)
	if err != nil {
		die("load config %s: %v", *confPath, err)
	}
	cfg.RepoDir = filepath.Dir(*confPath)
	if _, err := os.Stat(*confPath); os.IsNotExist(err) {
		die("clawdstacc.conf not found at %s — copy clawdstacc.conf.example and edit it.", *confPath)
	}

	exePath, _ := os.Executable()
	home, _ := os.UserHomeDir()
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")

	fmt.Println(blue("==> Pre-flight checks"))
	preflight(cfg)

	if err := os.MkdirAll(launchAgents, 0o755); err != nil {
		die("mkdir LaunchAgents: %v", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		die("mkdir log dir: %v", err)
	}

	// Write the embedded tmux config to its on-disk location. Plists and
	// the VSCode auto-attach task reference this file via -f, so it has to
	// exist before any agent boots.
	if err := writeTmuxConf(); err != nil {
		die("write tmux conf: %v", err)
	}
	fmt.Printf("  %s wrote %s\n", green("✓"), tmuxConfPath())

	// Resolve projects.
	projects := ListProjects(cfg)
	if len(projects) == 0 {
		die("no projects found. PROJECTS_GLOB=%q matched nothing.", cfg.ProjectsGlob)
	}

	fmt.Println(blue("==> Projects"))
	for _, p := range projects {
		fmt.Printf("  • %s\n", green(filepath.Base(p)))
	}

	// Migration: kill stale sessions that were created on the user's default
	// tmux socket by older clawdstacc versions (before we moved to
	// -L clawdstacc). kill-session targets the default server so unrelated
	// sessions on it are untouched.
	//
	// Self-exception: if we're running this from inside one of the project
	// sessions, skip its kill — committing suicide mid-setup leaves launchd
	// to respawn under the *old* plist before the loop has rendered the new
	// one, and we end up in an oscillating reset loop.
	ownSession := ""
	if os.Getenv("TMUX") != "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
			ownSession = strings.TrimSpace(string(out))
		}
	}
	for _, p := range projects {
		name := ProjectName(p)
		if name == ownSession {
			fmt.Printf("  %s skipping legacy-socket cleanup for %q (you're attached to it — exit later to finish the migration)\n",
				yellow("↻"), name)
			continue
		}
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	}

	// Per-project: render plist + .vscode (if code-server enabled).
	fmt.Println(blue("==> Generating per-project configuration"))
	var generated []string
	for _, p := range projects {
		name := ProjectName(p)
		generated = append(generated, name)

		// 1) launchd plist
		plistPath := filepath.Join(launchAgents, "com.user.clawdstacc."+name+".plist")
		if err := writePlist("claude.plist.tmpl", plistPath, projectVars(cfg, p, name, home)); err != nil {
			die("render claude.plist for %s: %v", name, err)
		}

		// 2) .vscode/ — only when code-server is on
		if cfg.EnableCodeServer {
			vscodeDir := filepath.Join(p, ".vscode")
			_ = os.MkdirAll(vscodeDir, 0o755)
			vars := map[string]string{"PROJECT_NAME": name}
			writeIfMissingOrOurs(filepath.Join(vscodeDir, "tasks.json"), "vscode-tasks.json.tmpl", vars, "claude --rc")
			writeIfMissingOrOurs(filepath.Join(vscodeDir, "settings.json"), "vscode-settings.json.tmpl", vars, "tmux-claude")
		}

		fmt.Printf("  %s %s\n", green("✓"), name)
	}

	// code-server plist (only when enabled)
	if cfg.EnableCodeServer {
		fmt.Println(blue("==> Generating code-server plist"))
		dst := filepath.Join(launchAgents, "com.user.clawdstacc.codeserver.plist")
		vars := map[string]string{
			"BREW_PREFIX":         cfg.BrewPrefix,
			"USER_HOME":           home,
			"LOG_DIR":             cfg.LogDir,
			"CODESERVER_BIND":     cfg.CodeServerBind,
			"CODESERVER_AUTH":     cfg.CodeServerAuth,
			"CODESERVER_PASSWORD": cfg.CodeServerPassword,
		}
		if err := writePlist("codeserver.plist.tmpl", dst, vars); err != nil {
			die("render codeserver plist: %v", err)
		}
		fmt.Println("  " + green("✓ codeserver.plist"))
	} else {
		fmt.Println(blue("==> Skipping code-server (ENABLE_CODESERVER=false → headless mode)"))
	}

	// Dashboard plist — points at THIS binary, dashboard subcommand.
	fmt.Println(blue("==> Generating dashboard plist"))
	dashboardPlist := filepath.Join(launchAgents, "com.user.clawdstacc.dashboard.plist")
	if err := writeDashboardPlist(dashboardPlist, exePath, *confPath, cfg, home); err != nil {
		die("render dashboard plist: %v", err)
	}
	fmt.Println("  " + green("✓ dashboard.plist"))

	// Compute desired plist set, find orphans, reload.
	wanted := []string{dashboardPlist}
	if cfg.EnableCodeServer {
		wanted = append(wanted, filepath.Join(launchAgents, "com.user.clawdstacc.codeserver.plist"))
	}
	for _, name := range generated {
		wanted = append(wanted, filepath.Join(launchAgents, "com.user.clawdstacc."+name+".plist"))
	}

	fmt.Println(blue("==> launchd: orphan cleanup + reload"))
	cleanOrphans(launchAgents, wanted)
	for _, plist := range wanted {
		_ = launchctlBootout(plist)
	}
	for _, plist := range wanted {
		if err := launchctlBootstrap(plist); err != nil {
			fmt.Printf("  %s failed to bootstrap %s: %v\n", red("✗"), filepath.Base(plist), err)
		} else {
			fmt.Printf("  %s loaded: %s\n", green("✓"), filepath.Base(plist))
		}
	}

	// Final summary.
	fmt.Println()
	fmt.Println(green("═══════════════════════════════════════════════════════════════"))
	fmt.Println(green("  Setup complete."))
	fmt.Println(green("═══════════════════════════════════════════════════════════════"))
	fmt.Println()
	fmt.Printf("  Status:     clawdstacc status\n")
	fmt.Printf("  Dashboard:  http://<host>:%s\n", cfg.DashboardPort)
	if cfg.EnableCodeServer {
		fmt.Printf("  code-server: http://<host>:%s\n", strings.TrimPrefix(cfg.CodeServerBind, "0.0.0.0:"))
	}
	fmt.Printf("  Logs:       tail -f %s/*.log\n", cfg.LogDir)
	fmt.Println()
	fmt.Println("On your phone: Claude app → Code tab. Sessions appear within seconds.")
}

// preflight verifies macOS + brew + required tools, offering brew installs
// for missing brew packages. Aborts (via die) on irrecoverable problems.
func preflight(cfg Config) {
	if got := osName(); got != "darwin" {
		die("clawdstacc targets macOS. You are on %s. Linux support is on the roadmap.", got)
	}
	brew := filepath.Join(cfg.BrewPrefix, "bin", "brew")
	if _, err := os.Stat(brew); err != nil {
		die("Homebrew not found at %s. Adjust BREW_PREFIX in clawdstacc.conf or install brew.", brew)
	}

	required := []string{"tmux", "go"}
	if cfg.EnableCodeServer {
		required = append(required, "code-server")
	}
	var missing []string
	for _, cmd := range required {
		if _, err := exec.LookPath(cmd); err != nil {
			missing = append(missing, cmd)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("%s Missing brew packages: %s\n", yellow("⚠"), strings.Join(missing, " "))
		if !promptYN(fmt.Sprintf("Install with 'brew install %s'?", strings.Join(missing, " "))) {
			die("Install missing tools and re-run.")
		}
		args := append([]string{"install"}, missing...)
		c := exec.Command(brew, args...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			die("brew install failed: %v", err)
		}
	}

	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Println(yellow("Claude Code CLI not found."))
		if !promptYN("Install with 'curl -fsSL https://claude.com/install.sh | bash'?") {
			die("Install Claude Code from https://claude.com/code and re-run.")
		}
		c := exec.Command("/bin/bash", "-c", "curl -fsSL https://claude.com/install.sh | bash")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			die("claude install failed: %v", err)
		}
		os.Setenv("PATH", filepath.Join(os.Getenv("HOME"), ".local/bin")+":"+os.Getenv("PATH"))
		if _, err := exec.LookPath("claude"); err != nil {
			die("claude still not on PATH after install. Add ~/.local/bin to your shell config and re-run.")
		}
	}

	if cfg.EnableCodeServer && cfg.CodeServerAuth == "password" && cfg.CodeServerPassword == "CHANGE_ME" {
		fmt.Println(yellow("WARNING: CODESERVER_PASSWORD is still 'CHANGE_ME'. Generate one with 'openssl rand -base64 24'."))
	}

	if cfg.EnableCodeServer {
		fmt.Println("  " + green("✓ macOS, brew, tmux, code-server, go, claude"))
	} else {
		fmt.Println("  " + green("✓ macOS, brew, tmux, go, claude") + gray("  (headless mode — code-server disabled)"))
	}
}

// projectVars returns the placeholder map used by claude.plist.tmpl and
// vscode-tasks.json.tmpl.
func projectVars(cfg Config, projectPath, name, home string) map[string]string {
	return map[string]string{
		"PROJECT_NAME":       name,
		"PROJECT_PATH":       projectPath,
		"USER_HOME":          home,
		"BREW_PREFIX":        cfg.BrewPrefix,
		"LOG_DIR":            cfg.LogDir,
		"CLAUDE_CONTINUE":    cfg.ClaudeContinue,
		"CLAUDE_EXTRA_FLAGS": cfg.ClaudeExtraFlags,
		"TMUX_SOCKET":        tmuxSocket,
		"TMUX_CONF":          tmuxConfPath(),
	}
}

// writeDashboardPlist generates the dashboard plist that launchd will use to
// run *this* binary as the dashboard subcommand. Stored as a separate helper
// because the template uses a special key (CLAWDSTACC_BIN) that the bash
// version never had.
func writeDashboardPlist(dst, exePath, confPath string, cfg Config, home string) error {
	vars := map[string]string{
		"BREW_PREFIX":     cfg.BrewPrefix,
		"USER_HOME":       home,
		"LOG_DIR":         cfg.LogDir,
		"CLAWDSTACC_BIN":  exePath,
		"CLAWDSTACC_CONF": confPath,
		"DASHBOARD_PORT":  cfg.DashboardPort,
	}
	return writePlist("dashboard.plist.tmpl", dst, vars)
}

// writePlist renders an embedded template and writes the result to dst.
func writePlist(tmplName, dst string, vars map[string]string) error {
	content, err := renderEmbeddedTemplate(tmplName, vars)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(content), 0o644)
}

// writeIfMissingOrOurs mirrors the bash check: write if file doesn't exist OR
// if it contains our marker string. Spares user-authored files.
func writeIfMissingOrOurs(dst, tmplName string, vars map[string]string, marker string) {
	if existing, err := os.ReadFile(dst); err == nil {
		if !strings.Contains(string(existing), marker) {
			fmt.Printf("  %s skipped (exists, not ours): %s\n", yellow("·"), dst)
			return
		}
	}
	if err := writePlist(tmplName, dst, vars); err != nil {
		fmt.Printf("  %s render %s: %v\n", red("✗"), dst, err)
	}
}

// cleanOrphans removes plist files we didn't generate — covers projects that
// were deleted from PROJECTS_GLOB / EXPLICIT_PROJECTS since the last run, and
// also code-server when ENABLE_CODESERVER flips to false.
func cleanOrphans(launchAgents string, wanted []string) {
	wantedSet := map[string]bool{}
	for _, w := range wanted {
		wantedSet[w] = true
	}
	matches, _ := filepath.Glob(filepath.Join(launchAgents, "com.user.clawdstacc.*.plist"))
	for _, m := range matches {
		if wantedSet[m] {
			continue
		}
		label := strings.TrimSuffix(filepath.Base(m), ".plist")
		_ = launchctlBootout(m)
		_ = os.Remove(m)
		fmt.Printf("  %s orphan removed: %s\n", yellow("✗"), label)
	}
}

func launchctlBootout(plistOrLabel string) error {
	uid := os.Getuid()
	label := plistOrLabel
	if strings.HasSuffix(plistOrLabel, ".plist") {
		label = strings.TrimSuffix(filepath.Base(plistOrLabel), ".plist")
	}
	return exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, label)).Run()
}

// launchctlBootstrap loads a plist into launchd, retrying transient errors.
// `Bootstrap failed: 5: Input/output error` happens when the previous instance
// hasn't fully released its port / service slot yet — three attempts with a
// 1s gap is what the old bash setup did and it's plenty.
func launchctlBootstrap(plist string) error {
	uid := os.Getuid()
	const attempts = 3
	var lastErr error
	for i := 1; i <= attempts; i++ {
		out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plist).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
		if i < attempts {
			time.Sleep(time.Second)
		}
	}
	return lastErr
}

func osName() string {
	out := sh("uname", "")
	return strings.ToLower(out)
}
