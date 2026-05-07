package clawd

import (
	"os"
	"os/exec"
	"syscall"
)

// cmdTmux is a transparent passthrough to `tmux -L clawdstacc -f <conf> ...`.
// We `syscall.Exec` so tmux owns the TTY directly — running it as a Go
// child would mangle terminal modes for the user's interactive attach.
//
// Examples:
//
//	clawdstacc tmux ls                  → list our sessions
//	clawdstacc tmux attach -t demo      → attach to a specific project
//	clawdstacc tmux kill-session -t foo → kill a session
//	clawdstacc tmux                     → bare tmux on our socket
func cmdTmux(args []string) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		die("tmux not on PATH — install with 'brew install tmux'")
	}
	// argv[0] must be the binary name; tmux uses it for things like
	// `pgrep`-friendly process naming.
	argv := append([]string{"tmux"}, tmuxArgs()...)
	argv = append(argv, args...)
	if err := syscall.Exec(tmuxBin, argv, os.Environ()); err != nil {
		die("exec tmux: %v", err)
	}
}
