package clawd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tmuxSocket is the dedicated server socket clawdstacc spawns its sessions on.
// Every tmux invocation — from the binary, from rendered plists, from the
// VSCode auto-attach task — passes `-L tmuxSocket -f tmuxConfPath()` so we
// never share state with the user's default-server tmux config.
const tmuxSocket = "clawdstacc"

//go:embed tmux.conf
var embeddedTmuxConf []byte

// tmuxConfPath returns the on-disk location of our tmux config. setup writes
// the embedded version there; the rest of the codebase just references it.
func tmuxConfPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clawdstacc", "tmux.conf")
}

// tmuxArgs returns the prefix args ("-L clawdstacc -f /path/to/tmux.conf")
// to inject before any subcommand. Use like:
//
//	exec.Command("tmux", append(tmuxArgs(), "has-session", "-t", name)...)
func tmuxArgs() []string {
	return []string{"-L", tmuxSocket, "-f", tmuxConfPath()}
}

// tmuxShPrefix returns the same prefix as a shell-quoted string — for
// embedding into the `sh` helper or shell-string construction. Mirrors
// `tmux -L clawdstacc -f <conf>`.
func tmuxShPrefix() string {
	return fmt.Sprintf("tmux -L %s -f %s", tmuxSocket, shellQuote(tmuxConfPath()))
}

// writeTmuxConf flushes the embedded tmux.conf to disk. Setup calls this
// before bootstrapping any agent so the socket spawns with our config.
// Idempotent — overwrites existing file each time so users can't drift from
// the embedded source. Customisation lives upstream; here we want determinism.
func writeTmuxConf() error {
	path := tmuxConfPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, embeddedTmuxConf, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// joinTmuxArgs is a tiny convenience for shell strings — useful when building
// a one-liner for a JSON template (e.g. vscode-tasks.json).
func joinTmuxArgs() string {
	return strings.Join(tmuxArgs(), " ")
}
