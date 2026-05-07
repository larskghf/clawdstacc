// clawdstacc — persistent Claude Code session manager for macOS.
// Subcommand dispatcher lives in internal/clawd; this is just the entrypoint.
package main

import "github.com/larskghf/clawdstacc/internal/clawd"

func main() {
	clawd.Main()
}
