# Contributing to clawdstacc

Thanks for considering a contribution. clawdstacc is small and personal in
scope, but PRs and issues are very welcome.

## Filing issues

Before opening a new issue, please check whether it's already tracked.

For bug reports, the more of these we have the faster we can help:

- macOS version + chip (Apple Silicon / Intel)
- Output of `./bin/clawdstacc status`
- Tail of relevant logs (`~/Library/Logs/clawdstacc/*.err`)
- Steps to reproduce

Use the `bug_report` template in `.github/ISSUE_TEMPLATE/` to skip the back-and-forth.

## Development setup

```bash
git clone https://github.com/<your-fork>/clawdstacc.git
cd clawdstacc
cp clawdstacc.conf.example clawdstacc.conf
$EDITOR clawdstacc.conf       # set CODESERVER_PASSWORD or CODESERVER_AUTH=none
./bin/clawdstacc setup
```

This builds the unified `bin/clawdstacc` binary, generates plists, and
registers everything with launchd. Re-running `./bin/clawdstacc setup` is
safe (idempotent).

### Working on the binary

```bash
go vet ./...
go test ./...
go build -o bin/clawdstacc .

# Apply changes (rebuilds + reloads only the dashboard agent)
launchctl kickstart -k "gui/$(id -u)/com.user.clawdstacc.dashboard"
```

Both the dashboard's HTML/CSS/JS (`web/`) and the launchd plist + VSCode
templates (`templates/`) are embedded via `go:embed` — single binary, no
asset paths to manage at runtime. Changes to either require a rebuild.

### Bash

The only shell script left is `install.sh` (the curl-pipe-bash bootstrap).
Run `shellcheck install.sh` before submitting. CI does the same.

## Conventions

- **Go** code passes `go vet` and `gofmt -s` cleanly.
- **Bash** in `install.sh` uses `set -euo pipefail` and stays compatible with
  macOS-default Bash 3.2 (no `mapfile`/`readarray`, no `${var,,}`).
- **Templates** use `__UPPERCASE__` placeholders, replaced by
  `renderEmbeddedTemplate` in `templates_embed.go`. New templates must follow
  the same scheme.
- **Plist labels** follow `com.user.clawdstacc.<role-or-name>`. Keep this
  consistent — `./bin/clawdstacc status` and the dashboard both parse it.
- **Logs** go to `~/Library/Logs/clawdstacc/<service>.{log,err}`.

## Commit messages

Short imperative sentence on the first line, optional body explaining *why*
rather than *what*. Reference issues with `Fixes #123` where appropriate.

## Pull requests

1. Fork → branch → commits → PR against `main`.
2. The CI workflow runs `go vet`, `go test`, `go build`, and `shellcheck`. PRs
   must be green.
3. Update `CHANGELOG.md` under the `[Unreleased]` section if your change is
   user-visible.
4. If you change the on-disk shape (config keys, plist labels, log dirs),
   update both the docs and the migration story.

## Scope

clawdstacc is intentionally narrow: a launchd-based persistent-Claude-Code
session manager for macOS, with a status dashboard. Things that don't fit:

- Heavy multi-tenant features (RBAC, billing, admin panel)
- Containerisation as a hard requirement (we deliberately run native)
- Tight coupling to one cloud provider (auth and tunneling stay pluggable)

Outside contributions for Linux support (`systemd` parallel to launchd) are
explicitly welcome.

## Code of Conduct

By participating in this project you agree to abide by the
[Code of Conduct](./CODE_OF_CONDUCT.md).
