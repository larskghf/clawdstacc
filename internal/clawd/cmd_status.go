package clawd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdStatus prints a human-readable, coloured snapshot of every component.
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	confPath := fs.String("conf", defaultConfPath(), "path to clawdstacc.conf")
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}

	cfg, err := LoadConfig(*confPath)
	if err != nil {
		die("load config %s: %v", *confPath, err)
	}
	cfg.RepoDir = filepath.Dir(*confPath)

	snap := CollectStatus(cfg)

	fmt.Println()
	fmt.Println(bold("╔══════════════════════════════════════════════════════════════╗"))
	fmt.Println(bold("║                  clawdstacc status                           ║"))
	fmt.Println(bold("╚══════════════════════════════════════════════════════════════╝"))
	fmt.Println()

	// code-server line (only when enabled or loaded)
	if cfg.EnableCodeServer || snap.CodeServerLoaded {
		fmt.Printf("  %-30s ", "code-server")
		if snap.CodeServerLoaded {
			fmt.Printf("%s running  %s(%s)%s\n", green("● "), cGray, cfg.CodeServerBind, cReset)
		} else {
			fmt.Println(red("● not loaded"))
		}
	}

	// Dashboard line
	fmt.Printf("  %-30s ", "dashboard")
	if agentLoaded("com.user.clawdstacc.dashboard") {
		fmt.Printf("%s running  %s(port %s)%s\n", green("● "), cGray, cfg.DashboardPort, cReset)
	} else {
		fmt.Println(red("● not loaded"))
	}
	fmt.Println()

	// Projects table
	fmt.Println(bold("Projects"))
	fmt.Println(gray("──────────────────────────────────────────────────────────────"))
	if len(snap.Projects) == 0 {
		fmt.Println("  " + yellow("No projects matched.  PROJECTS_GLOB="+cfg.ProjectsGlob))
	} else {
		for _, p := range snap.Projects {
			line := fmt.Sprintf("  %-26s %s tmux  %s claude  %s agent",
				p.Name,
				dot(p.TmuxAlive), dot(p.ClaudeAlive), dot(p.AgentLoaded))
			if p.Session != nil {
				line += "   " + gray("last activity: "+fmtAgo(p.Session.ModifiedAgo))
				if p.Session.OpenToolUse != "" {
					line += "   " + blue("▶ "+p.Session.OpenToolUse)
				}
			}
			fmt.Println(line)
		}
	}

	fmt.Println()
	fmt.Println(bold("Logs"))
	fmt.Println(gray("──────────────────────────────────────────────────────────────"))
	fmt.Printf("  %s\n", cfg.LogDir)

	if entries, err := os.ReadDir(cfg.LogDir); err == nil {
		var nonEmptyErrs []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if filepath.Ext(e.Name()) != ".err" {
				continue
			}
			info, err := e.Info()
			if err == nil && info.Size() > 0 {
				nonEmptyErrs = append(nonEmptyErrs, e.Name())
			}
		}
		if len(nonEmptyErrs) > 0 {
			fmt.Printf("  %s non-empty .err files: %d\n", yellow("⚠"), len(nonEmptyErrs))
			for _, n := range nonEmptyErrs {
				fmt.Printf("    - %s\n", n)
			}
		} else {
			fmt.Println("  " + green("✓ no errors in logs"))
		}
	}
	fmt.Println()
}

func dot(ok bool) string {
	if ok {
		return green("●")
	}
	return red("●")
}
