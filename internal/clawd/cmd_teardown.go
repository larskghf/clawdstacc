package clawd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdTeardown stops every clawdstacc launchd agent and deletes its plist.
// Leaves .vscode/ folders, tmux sessions, and ~/.claude/ history untouched.
func cmdTeardown(args []string) {
	fs := flag.NewFlagSet("teardown", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}

	home, _ := os.UserHomeDir()
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")

	matches, _ := filepath.Glob(filepath.Join(launchAgents, "com.user.clawdstacc.*.plist"))
	if len(matches) == 0 {
		fmt.Println(yellow("Nothing to remove — no clawdstacc agents found."))
		return
	}

	fmt.Println(yellow("This will stop and remove the following launchd agents:"))
	for _, m := range matches {
		fmt.Printf("  - %s\n", filepath.Base(m))
	}
	fmt.Println()

	if !*yes && !promptYN("Continue?") {
		fmt.Println(red("Aborted."))
		os.Exit(1)
	}

	for _, m := range matches {
		label := strings.TrimSuffix(filepath.Base(m), ".plist")
		_ = launchctlBootout(m)
		if err := os.Remove(m); err != nil {
			fmt.Printf("  %s failed to remove %s: %v\n", red("✗"), label, err)
		} else {
			fmt.Printf("  %s removed: %s\n", green("✓"), label)
		}
	}

	fmt.Println()
	fmt.Println(yellow("Optional follow-ups (not done automatically):"))
	fmt.Println("  tmux kill-server                            # close all tmux sessions")
	fmt.Println("  rm -rf <project>/.vscode                    # remove generated VSCode configs")
}
