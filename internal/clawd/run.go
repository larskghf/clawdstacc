package clawd

import (
	"fmt"
	"os"
)

const usage = `clawdstacc — persistent Claude Code session manager

Usage:
  clawdstacc <command> [flags]

Commands:
  setup        Render plists, register launchd agents, build templates
  status       CLI overview of every component
  tmux         Wrapper around 'tmux -L clawdstacc -f <conf>' (attach/list/etc.)
  remove       Stop and remove a single project's launchd agent + generated files
  teardown     Stop and remove all clawdstacc launchd agents
  dashboard    Run the web dashboard in the foreground (launchd uses this)
  version      Print version information
  help         Show this help

Run 'clawdstacc <command> --help' for command-specific flags.

Environment:
  CLAWDSTACC_CONF   path to clawdstacc.conf (default: <binary-dir>/../clawdstacc.conf)
  CLAWDSTACC_PORT   default port for the dashboard subcommand (default: 8390)
  CLAWDSTACC_YES=1  non-interactive; assume yes for prompts in 'setup'/'teardown'

Examples:
  clawdstacc setup            # first-time install + plist registration
  clawdstacc status           # what's loaded, what's running, what's idle
  clawdstacc setup            # idempotent — re-render after editing the conf
  clawdstacc teardown         # stop and remove every clawdstacc agent
`

// Main is the subcommand dispatcher. The thin cmd/clawdstacc/main.go calls
// us; everything else lives in this package.
func Main() {
	args := os.Args[1:]

	// No args → show help. Friendlier than the previous default (start the
	// dashboard and crash on EADDRINUSE because launchd already has the port).
	// launchd's plist always passes "dashboard" explicitly, so it isn't
	// affected by this change.
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}

	// Top-level flags handled before subcommand dispatch.
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Print(usage)
		return
	case "--version", "-v":
		cmdVersion()
		return
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "dashboard":
		cmdDashboard(args)
	case "setup":
		cmdSetup(args)
	case "teardown":
		cmdTeardown(args)
	case "remove", "rm":
		cmdRemove(args)
	case "tmux":
		cmdTmux(args)
	case "status":
		cmdStatus(args)
	case "version":
		cmdVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}
