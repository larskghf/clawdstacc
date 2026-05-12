package clawd

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// cmdReload is the post-binary-swap idempotent refresh.
//
// Use case: `brew upgrade clawdstacc` swaps the binary in /opt/homebrew/bin
// but doesn't touch launchd or restart the dashboard. The running dashboard
// process still has the old code/templates in memory, and per-project plists
// only need re-rendering when the embedded plist template actually changed.
//
// reload re-renders every plist in memory, diffs against disk, writes +
// bootout/bootstrap only the ones that changed (so unchanged claude sessions
// stay alive across a no-op upgrade), and always kickstarts the dashboard
// because its in-memory state is guaranteed stale after a binary swap.
//
// Safe to run any time. No-op (with a friendly hint) when no clawdstacc
// agents are installed yet — that's the install-time path where brew's
// post_install hook fires before the user has run setup.
func cmdReload(args []string) {
	fs := flag.NewFlagSet("reload", flag.ExitOnError)
	confPath := fs.String("conf", defaultConfPath(), "path to clawdstacc.conf")
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}
	if abs, err := filepath.Abs(*confPath); err == nil {
		*confPath = abs
	}

	home, _ := os.UserHomeDir()
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")

	// Fresh install path: nothing to reload yet, exit 0 quietly so brew's
	// post_install doesn't choke.
	matches, _ := filepath.Glob(filepath.Join(launchAgents, "com.user.clawdstacc.*.plist"))
	if len(matches) == 0 {
		fmt.Println(gray("clawdstacc reload: no agents installed yet — run 'clawdstacc setup' to get started."))
		return
	}

	// No conf yet (user upgraded before ever running setup) → also a no-op,
	// not an error.
	if _, err := os.Stat(*confPath); os.IsNotExist(err) {
		fmt.Println(gray("clawdstacc reload: no config at " + *confPath + " — run 'clawdstacc setup' first."))
		return
	}
	cfg, err := LoadConfig(*confPath)
	if err != nil {
		die("load config %s: %v", *confPath, err)
	}
	cfg.RepoDir = filepath.Dir(*confPath)

	exePath, _ := os.Executable()

	// Render every plist we'd manage, in memory.
	type rendered struct {
		path    string
		content []byte
	}
	var plists []rendered

	for _, p := range ListProjects(cfg) {
		name := ProjectName(p)
		dst := filepath.Join(launchAgents, "com.user.clawdstacc."+name+".plist")
		content, err := renderEmbeddedTemplate("claude.plist.tmpl", projectVars(cfg, p, name, home))
		if err != nil {
			fmt.Printf("  %s render %s: %v\n", red("✗"), name, err)
			continue
		}
		plists = append(plists, rendered{path: dst, content: []byte(content)})

		// .vscode/settings.json + tasks.json — re-render so embedded template
		// changes (new keys, fixed paths) propagate after a brew upgrade
		// without requiring a manual `clawdstacc setup`.
		if cfg.EnableCodeServer {
			vscodeDir := filepath.Join(p, ".vscode")
			_ = os.MkdirAll(vscodeDir, 0o755)
			vars := map[string]string{
				"PROJECT_NAME": name,
				"TMUX_SOCKET":  tmuxSocket,
				"TMUX_CONF":    tmuxConfPath(),
			}
			writeIfMissingOrOurs(filepath.Join(vscodeDir, "tasks.json"), "vscode-tasks.json.tmpl", vars, "Attach Claude (tmux)")
			writeIfMissingOrOurs(filepath.Join(vscodeDir, "settings.json"), "vscode-settings.json.tmpl", vars, "tmux-claude")
		}
	}

	if cfg.EnableCodeServer {
		dst := filepath.Join(launchAgents, "com.user.clawdstacc.codeserver.plist")
		content, err := renderEmbeddedTemplate("codeserver.plist.tmpl", map[string]string{
			"BREW_PREFIX":         cfg.BrewPrefix,
			"USER_HOME":           home,
			"LOG_DIR":             cfg.LogDir,
			"CODESERVER_BIND":     cfg.CodeServerBind,
			"CODESERVER_AUTH":     cfg.CodeServerAuth,
			"CODESERVER_PASSWORD": cfg.CodeServerPassword,
		})
		if err == nil {
			plists = append(plists, rendered{path: dst, content: []byte(content)})
		}
	}

	dashboardPlist := filepath.Join(launchAgents, "com.user.clawdstacc.dashboard.plist")
	if content, err := renderEmbeddedTemplate("dashboard.plist.tmpl", map[string]string{
		"BREW_PREFIX":     cfg.BrewPrefix,
		"USER_HOME":       home,
		"LOG_DIR":         cfg.LogDir,
		"CLAWDSTACC_BIN":  exePath,
		"CLAWDSTACC_CONF": *confPath,
		"DASHBOARD_PORT":  cfg.DashboardPort,
	}); err == nil {
		plists = append(plists, rendered{path: dashboardPlist, content: []byte(content)})
	}

	// Diff. Write + bootout/bootstrap only the changed ones — claude sessions
	// on unchanged plists keep running.
	fmt.Println(blue("==> Reloading clawdstacc"))
	var restartedDashboard bool
	changed := 0
	for _, r := range plists {
		existing, err := os.ReadFile(r.path)
		if err == nil && bytes.Equal(existing, r.content) {
			continue
		}
		if err := os.WriteFile(r.path, r.content, 0o644); err != nil {
			fmt.Printf("  %s write %s: %v\n", red("✗"), filepath.Base(r.path), err)
			continue
		}
		_ = launchctlBootout(r.path)
		if err := launchctlBootstrap(r.path); err != nil {
			fmt.Printf("  %s reload %s: %v\n", red("✗"), filepath.Base(r.path), err)
			continue
		}
		fmt.Printf("  %s reloaded: %s\n", green("✓"), filepath.Base(r.path))
		changed++
		if r.path == dashboardPlist {
			restartedDashboard = true
		}
	}
	if changed == 0 {
		fmt.Println("  " + gray("all plists already current"))
	}

	// Dashboard binary is in /opt/homebrew/bin, swapped in place by brew.
	// The running launchd process still holds the old binary's templates in
	// memory — kickstart picks up the new one. Skip if we already restarted
	// it above via bootout/bootstrap.
	if !restartedDashboard {
		if _, err := os.Stat(dashboardPlist); err == nil {
			uid := os.Getuid()
			label := "com.user.clawdstacc.dashboard"
			if err := exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, label)).Run(); err != nil {
				fmt.Printf("  %s kickstart dashboard: %v\n", red("✗"), err)
			} else {
				fmt.Printf("  %s dashboard refreshed\n", green("✓"))
			}
		}
	}

	fmt.Println(green("Reload complete."))
}
