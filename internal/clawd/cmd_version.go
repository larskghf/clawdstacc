package clawd

import "fmt"

// Set via -ldflags at build time. See .goreleaser.yml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func cmdVersion() {
	fmt.Printf("clawdstacc %s\n  commit: %s\n  built:  %s\n", version, commit, date)
}
