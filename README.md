# clawdstacc

Persistent, remote-controllable Claude Code sessions on a macOS host (Mac mini, workstation), reachable from your phone, browser and laptop.

A single code-server instance hosts every project in the browser. Each project has its own `claude --rc` process running inside a tmux session, started and watched by launchd. Every session is reachable through the Claude iOS/Android app as a Remote Control session. A dashboard shows the status of all sessions and offers a one-click restart.

---

## What you get

- Host boots → every session starts automatically
- Phone: Claude app → Code tab → your projects, each as a Remote Control session
- Browser (LAN or VPN): `http://<host>:8443` → VSCode with all projects; the integrated terminal auto-attaches to the running Claude session
- Dashboard: `http://<host>:8390` → status overview, restart buttons
- All slash commands (`/clear`, `/compact`, …) work from anywhere
- Sessions survive reboots via `claude --continue`
- If a tmux session dies, launchd respawns it within ~10 seconds
- Full zsh login shell (Oh My Zsh, theme, plugins) in both the VSCode terminal and the tmux pane

## Quickstart

```bash
git clone <your-repo-url> ~/clawdstacc
cd ~/clawdstacc

# Create the config (one-time)
cp clawdstacc.conf.example clawdstacc.conf
$EDITOR clawdstacc.conf       # at minimum, set CODESERVER_PASSWORD

# Recommended: tmux config so new panes/windows are login shells
cp tmux.conf.example ~/.tmux.conf

# Run the setup — generates plists and VSCode configs, loads launchd
./bin/setup.sh

# Verify
./bin/status.sh
```

After setup, open `http://<host>:8443` (code-server) and `http://<host>:8390` (dashboard) in a browser.

## Requirements

- macOS (tested on Apple Silicon; should also work on Intel — set `BREW_PREFIX` in the config)
- [Homebrew](https://brew.sh)
- `tmux`, `code-server`, `go` (build-time only, for the dashboard), `claude` (the Claude Code CLI) — `setup.sh` checks and reports anything missing
- A Pro or Max plan for claude.ai (Remote Control needs it)
- Some VPN into your home network if you want to reach the stack from outside (Wireguard on the router, OpenVPN, Tailscale, …) — clawdstacc is VPN-agnostic; everything binds on `0.0.0.0` and is reachable by IP

Install:

```bash
brew install tmux code-server go
curl -fsSL https://claude.com/install.sh | bash
claude       # run /login once
```

## Repository layout

```
clawdstacc/
├── README.md                  # this file (setup + architecture + dev notes)
├── clawdstacc.conf.example         # config template (copy to clawdstacc.conf)
├── tmux.conf.example          # recommended ~/.tmux.conf (optional)
├── bin/
│   ├── setup.sh         # main script — renders plists/configs, loads launchd
│   ├── teardown.sh      # remove everything
│   ├── status.sh        # CLI status overview
│   └── dashboard              # built Go binary (gitignored — produced by setup.sh)
├── dashboard/                 # Go source for the web dashboard (HTMX + SSE)
│   ├── main.go, server.go, status.go, jsonl.go, config.go, projects.go, util.go
│   └── web/                   # embedded HTML templates
├── templates/
│   ├── claude.plist.tmpl      # tmux+claude per project
│   ├── codeserver.plist.tmpl  # code-server (single instance)
│   ├── dashboard.plist.tmpl   # dashboard service
│   ├── vscode-tasks.json.tmpl # auto-attach to tmux on folderOpen
│   └── vscode-settings.json.tmpl # zsh-login default profile + tmux-claude
└── docs/
    ├── SETUP.md               # detailed setup walkthrough
    └── TROUBLESHOOTING.md     # common problems and fixes
```

## Day-to-day commands

```bash
# Status of every component
./bin/status.sh

# Restart one session manually
launchctl kickstart -k "gui/$(id -u)/com.user.clawdstacc.<projectname>"

# Tail the logs
tail -f ~/Library/Logs/clawdstacc/*.log

# Re-render the stack (e.g. after editing clawdstacc.conf)
./bin/setup.sh

# Tear it all down
./bin/teardown.sh
```

---

## Architecture

### Design goals

1. **Persistence:** sessions survive browser tabs, network drops and reboots
2. **Multi-access:** phone app, browser VSCode, SSH+tmux all attach to the same session
3. **Native — no container/VM:** macOS launchd manages everything directly, so Claude and tools have full filesystem and MCP access
4. **Idempotent:** `setup.sh` can run any number of times; everything regenerates from templates
5. **Self-healing:** launchd respawns the per-project script when the tmux session dies, and the script recreates the session when it starts

### Layers

```
┌──────────────────────────────────────────────────────────┐
│  Phone (Claude App)    Browser (code-server)    Laptop   │
│        │                      │                   │      │
│        │ Remote Control       │ HTTPS             │ SSH  │
│        │ (Anthropic API)      │                   │      │
└────────┼──────────────────────┼───────────────────┼──────┘
         │                      │                   │
         ▼                      ▼                   ▼
┌──────────────────────────────────────────────────────────┐
│                     Mac (macOS)                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │  launchd (init system, starts every service)     │    │
│  └─────┬───────────────┬───────────────┬────────────┘    │
│        │               │               │                 │
│  ┌─────▼─────┐  ┌──────▼──────┐  ┌────▼───────┐         │
│  │ tmux      │  │ code-server │  │ dashboard  │         │
│  │ session   │  │ (port 8443) │  │ (port 8390)│         │
│  │ ─────────│  └─────────────┘  └────────────┘         │
│  │ claude    │                                          │
│  │ --rc      │                                          │
│  └───────────┘                                          │
│                                                         │
│  ~/.claude/projects/-USERS-NAME-_project/               │
│    └─ session-<uuid>.jsonl   (conversation history)     │
└──────────────────────────────────────────────────────────┘
```

### Components

**launchd** is the macOS init system. Three plist types live in `~/Library/LaunchAgents/`:

- `com.user.clawdstacc.<name>.plist` — one per project, runs tmux+claude
- `com.user.clawdstacc.codeserver.plist` — single instance, code-server on `~`
- `com.user.clawdstacc.dashboard.plist` — single instance, status dashboard

All plists set `RunAtLoad: true` (start at login) and `KeepAlive: true` (always respawn). The per-project plist is structured so that the script blocks until the tmux session dies, which gives launchd a clean signal to respawn it (see "Self-healing" below).

**tmux** is the persistence layer for `claude`. One session per project, named after the directory minus a leading underscore. Sessions live independently of any frontend — close the browser tab, drop the SSH connection, doesn't matter.

**claude --rc** is Claude Code with Remote Control enabled. Every instance registers with the Anthropic API and shows up as its own session in the Claude app. We pass `--name <projectname>` and `--remote-control-session-name-prefix <projectname>` so each session has a stable, recognizable name (the `--name` is set fresh on every start, even with `--continue`, so old persisted names get overwritten).

**code-server** is VSCode in the browser, mounted on `~`. Every project gets a `.vscode/`:
- `tasks.json` — `runOn: folderOpen` opens a terminal panel that runs `tmux attach -t <name>`
- `settings.json` — `zsh-login` as the default terminal profile (full login shell with Oh My Zsh), plus a `tmux-claude` profile, plus `workbench.panel.defaultLocation: right` so the terminal lives on the right

**dashboard** is a Go web server on port 8390 (source in `dashboard/`, built into `bin/dashboard` by `setup.sh`). It shows the status of every component, streams live updates via HTMX + Server-Sent-Events, and exposes `POST /api/restart/<name>` which calls `launchctl kickstart -k`. HTML templates are embedded in the binary via `go:embed` — no runtime asset paths to manage.

### Data flow: a question from the phone

1. User taps a project in the Claude app and types a question
2. Claude app sends an HTTPS request to the Anthropic API
3. The API routes it to the registered `claude --rc` process on the host, over the outbound HTTPS connection that process keeps open (no inbound ports needed)
4. The process runs locally — file reads, tool calls, MCP calls
5. The response flows back through the same path

If the same project is open in a code-server browser tab, the response shows up live in the integrated terminal too — the tmux attach there is wired to the same `claude` process.

### Self-healing

The per-project plist sets `KeepAlive: true` and `ThrottleInterval: 10`. The CDATA bash script:

1. Creates the tmux session running just `$CLAUDE_CMD` if it doesn't exist (and writes `.claude/.has-session` so `--continue` is used next time)
2. `while tmux has-session; do sleep 10; done`
3. `exit 1` once the loop ends

When `claude` exits — `/exit`, crash or external `tmux kill-session` — the pane closes, the only window is gone, the session ends, the watcher returns, the script exits non-zero, launchd respawns. End-to-end recovery measured at ~8 seconds.

Trade-off: the tmux pane no longer falls back to a login shell when Claude dies, so you can't `tmux attach` and poke around in zsh in that state. If you need to inspect, run `tmux kill-session` to force a restart, or temporarily change the plist.

### Shell environment handling

launchd-started processes do not inherit your shell environment — no `~/.zshrc`, no Oh My Zsh, no PATH from your shell config. To keep terminals usable, we set login shells in two places:

1. **vscode-settings.json.tmpl** uses `zsh-login` (`-l -i`) as the default terminal profile in code-server, so integrated terminals load your full shell config.
2. **tmux.conf.example** (optional) sets `default-command "/bin/zsh -l"` so new windows inside a tmux session also start as login shells.

This is the most common cause of "my terminal looks broken" on first setup — see `docs/TROUBLESHOOTING.md`.

### Persistence marker for `--continue`

`claude --continue` requires a previous session. There is none on the very first start — so `--continue` would fail. We use a marker file `<project>/.claude/.has-session`:

- First start: marker missing → `claude` (no `--continue`) → after a successful start the marker is written
- Later starts: marker present → `claude --continue` → resume the last conversation

Logic lives in `templates/claude.plist.tmpl`. If `~/.claude/projects/` is cleaned up externally, the marker can drift out of sync — see TROUBLESHOOTING.

### Label naming

Every plist label starts with `com.user.clawdstacc.` so they group together in `launchctl list` and don't collide with anything else. `setup.sh` cleans up plists that don't match the current configuration (orphans from removed projects, plus the older `com.user.claude.*` labels for migrated installs).

---

## Development & extension

### Conventions

- **Bash scripts** in `bin/` use `set -euo pipefail` and are compatible with the macOS-default Bash 3.2 (no `mapfile`/`readarray`, no `${var,,}`).
- **Templates** use `__UPPERCASE__` placeholders, replaced by `render_template()` in `setup.sh`. New templates should follow the same scheme.
- **Plist labels** follow `com.user.clawdstacc.<role-or-name>`. Keep this consistent — `status.sh` and the Go dashboard parse it.
- **Logs** go to `~/Library/Logs/clawdstacc/<service>.{log,err}`. Don't write to `/tmp` (volatile).
- **Go dashboard** uses stdlib only (`net/http`, `html/template`, `embed`) plus HTMX from CDN. Resist adding dependencies; if you must, document why.

### Common extensions

**Add a new config option:**
1. Document the default in `clawdstacc.conf.example`
2. In `setup.sh`, default it via `: "${VAR:=default}"`
3. If used by templates: extend the `render_template` calls with `KEY=val`
4. If the dashboard cares about it: extend `LoadConfig()` in `dashboard/config.go`

**Add a new service to the stack** (e.g. an MCP bridge, another web server):
1. Write `templates/<service>.plist.tmpl`
2. Add a `render_template` call in `setup.sh`
3. Add the plist path to `ALL_PLISTS` so it gets reloaded
4. Add a status check in `status.sh` and `dashboard/status.go`
5. Teardown picks it up automatically as long as the label matches `com.user.clawdstacc.*`

**Per-project customization** (e.g. different claude flags per project):
- Currently global via `CLAUDE_EXTRA_FLAGS`. Possible extension: read `<project>/.clawdstacc.yml` in `setup.sh` and override at template-render time.

**Extend the dashboard:**
- Front-end lives in `dashboard/web/*.html` (HTMX-driven). Keep it small — if it grows, split into more `{{define}}` blocks.
- API endpoints live under `/api/`. Validate inputs strictly (see `RestartProject()` for the pattern).
- Live updates flow over `/sse/status` as HTML fragments swapped by HTMX.
- Per-project stop/start could be added (`/api/stop/<name>`, `/api/start/<name>`).

### Things deliberately not included

- **No TLS:** code-server and the dashboard speak plain HTTP. The recommendation is a VPN, not direct internet exposure. If you need TLS, put a reverse proxy in front (Caddy, nginx) or use Tailscale Serve.
- **No auth on the dashboard:** the network is the auth boundary (VPN). If you ever expose the dashboard outside the LAN, add an auth layer.
- **No container/VM:** deliberate — see the architecture section.
- **No VSCode Settings Sync:** code-server's Open VSX and Microsoft's marketplace aren't sync-compatible. Install your extensions in code-server manually.
- **No automatic updates:** tools update via `brew upgrade`, not via this repo. Recommendation: `brew pin code-server tmux` for stability, then unpin + upgrade explicitly.

### Tests / verification

No automated tests — this setup is macOS-specific and hardware-dependent. Manual verification:

```bash
# Syntax / build checks
bash -n bin/*.sh
( cd dashboard && go vet ./... && go build -o /tmp/dashboard . )

# Smoke test (on macOS with prerequisites installed)
./bin/setup.sh
./bin/status.sh         # everything green?
curl -sf http://localhost:8390/api/status | python3 -m json.tool
./bin/teardown.sh       # clean removal?
```

A future direction would be a ShellCheck CI pipeline for the bash scripts and possibly a dry-run mode for `setup.sh` that prints the plists it would write instead of writing them.

## License

MIT.
