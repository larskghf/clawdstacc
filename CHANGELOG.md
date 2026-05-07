# Changelog

All notable changes to clawdstacc are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Dedicated tmux server socket (`tmux -L clawdstacc`)**. Clawdstacc no
  longer touches your personal `~/.tmux.conf` or the default tmux server.
  Its config is embedded in the binary, written once to
  `~/.config/clawdstacc/tmux.conf`, and used only on the dedicated socket.
  New `clawdstacc tmux <args>` subcommand wraps the right `-L … -f …` flags
  for interactive use (e.g. `clawdstacc tmux attach -t <project>`).
- **Homebrew install** as the recommended path:
  `brew tap larskghf/tap && brew install clawdstacc`. The formula pulls in
  `tmux` and `code-server` automatically and stashes the conf example under
  `$(brew --prefix)/etc/clawdstacc/`. Auto-published by GoReleaser to
  `larskghf/homebrew-tap` on every tagged release.
- **Unified `clawdstacc` Go binary** with subcommands (`dashboard`, `setup`,
  `status`, `remove`, `teardown`, `version`). Replaces the previous bash-script
  trio (`bin/setup.sh`, `bin/teardown.sh`, `bin/status.sh`). Templates are
  embedded via `go:embed` — one binary, no asset paths at runtime
- **`remove` subcommand + dashboard button** to undo a single project's
  registration: stops the agent, deletes its plist, kills the tmux session,
  and removes generated `.vscode/` files. Project directory and Claude
  conversation history are kept untouched
- Go-based dashboard binary (replaces stdlib Python dashboard)
- HTMX + Server-Sent-Events for live status updates without polling
- Auto-discovery of new projects from `PROJECTS_GLOB`; one-click setup per project
- Per-card paste target picker — Cmd+V/Ctrl+V on the dashboard sends the image
  to a chosen project via `tmux send-keys`, no Universal Clipboard required
- Code-server "auth = none" mode for setups protected by an upstream auth layer
  (Cloudflare Access, Tailscale, …)
- `CODESERVER_PUBLIC_URL` config — dashboard's "code-server ↗" links honour
  reverse-proxy / tunnel public URLs when accessed over HTTPS
- Activity-based card sort: most recently active session first
- Tool-args preview pill while a tool is running (e.g. `▶ Bash: npm test`)
- Model badge per card (`Opus 4.7`, `Sonnet 4.6`, …)
- Permission-mode badge when `bypassPermissions` is active

### Changed
- Renamed from `claude-stack` to `clawdstacc` (project, launchd labels, log dir)
- Bash scripts: `setup-stack.sh` → `setup.sh`, `teardown-stack.sh` → `teardown.sh`,
  `stack-status.sh` → `status.sh`
- Config file: `stack.conf` → `clawdstacc.conf`
- launchd labels: `com.user.claude-stack.*` → `com.user.clawdstacc.*`
- Log directory: `~/Library/Logs/claude-stack` → `~/Library/Logs/clawdstacc`

### Fixed
- JSONL session-dir path-mangling: underscores are now correctly translated to
  dashes (`/Users/jane/_demo` → `-Users-jane--demo`), so projects
  with leading underscores are no longer reported as "no session yet"
- "Open tool" detection no longer triggers on tail-window artefacts (a tool_use
  whose tool_result lives outside the read window is no longer flagged as open)

## [0.1.0] — initial private setup

Initial macOS-only stack with stdlib Python dashboard, hand-written launchd
plists, and per-project tmux+claude sessions.
