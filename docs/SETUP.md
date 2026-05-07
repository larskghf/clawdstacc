# Setup walkthrough

Step-by-step instructions for setting up clawdstacc on a Mac mini (or any other macOS host).

## 1. Prepare the host

### Prevent sleep

So the host actually keeps your sessions running:

```bash
sudo pmset -a sleep 0
sudo pmset -a disablesleep 1
sudo pmset -a powernap 1
sudo pmset -a womp 1
```

In System Settings → Battery / Energy, also enable "Prevent automatic sleeping when display is off". The display can sleep — only the system needs to stay awake.

### Static IP on the LAN

For stable access give the host a DHCP reservation in your router. Most routers (Fritzbox, Unifi, OPNsense, …) offer this under "DHCP server / Static leases / IP reservation". The host's MAC address is in System Settings → Network → Details → Hardware.

Note the IP — you'll reach code-server and the dashboard at it.

### Remote access from outside

If you also want to reach the host from mobile data or someone else's WiFi, you need a VPN into your home network. clawdstacc is agnostic — anything works as long as the host is reachable by IP. Common options:

- **Wireguard on the router** (Fritzbox 7590+, OPNsense, Unifi UDM, etc.) — recommended if your router supports it
- **OpenVPN on the router** — older but stable
- **Wireguard/OpenVPN on a Linux box on the LAN** — if your router can't VPN
- **Apple's built-in IPSec** — works, but fiddly to configure
- **Tailscale** — easiest if you don't want to run your own VPN

The VPN tunnel just needs to make the host reachable by IP. Once connected, `ping <host-ip>` should work — that's enough.

clawdstacc itself does not expose any ports to the public internet. Everything binds on `0.0.0.0`, meaning it's reachable on the LAN but not from outside. Over the VPN you're logically on the LAN and reach things normally.

## 2. Install tools

```bash
# Homebrew (skip if already installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Stack dependencies
brew install tmux code-server python@3.12

# Claude Code CLI
# (native installer — see https://claude.com/code for current methods)
curl -fsSL https://claude.com/install.sh | bash
```

After installing Claude Code, log in once:

```bash
claude
# follow /login — Pro or Max plan required for Remote Control
```

## 3. Shell setup for tmux and code-server (important!)

Background: launchd starts services with a minimal environment — `~/.zshrc` is not read, no Oh My Zsh, no PATH from your shell config. To keep terminals in tmux and the integrated code-server terminal usable with your normal zsh setup:

**a) `vscode-settings.json.tmpl` (deployed automatically by setup) sets `zsh -l -i` as the default profile** — the integrated terminal in code-server starts as a login shell, loads `.zprofile` and `.zshrc`, picks up your theme, plugins and aliases. This happens automatically.

**b) tmux config is automatic now.** Clawdstacc spawns its sessions on a dedicated tmux server socket (`tmux -L clawdstacc`) using its own embedded config. New windows (Ctrl+b c) inside those sessions inherit `default-command "${SHELL} -l"` — login shells, your zsh setup loads. Your personal `~/.tmux.conf` is **not** touched and is **not** loaded into clawdstacc sessions.

To attach to a clawdstacc session by hand: `clawdstacc tmux attach -t <project>`.

**Verify:** attach to a tmux session and check:

```bash
tmux attach -t project1
# inside: Ctrl+b c for a new window
echo $SHELL              # /bin/zsh
echo $ZSH                # should show your oh-my-zsh path
which brew               # should be found
```

If all three answers look right, your shell environment is correctly available in tmux.

## 4. Lay out project folders

clawdstacc expects every project in a folder with a leading underscore in your home directory (or any other glob you set in `clawdstacc.conf`). Example:

```
~/_project1/    # main project
~/_project2/
~/_clientwork/
```

The underscore makes stack-managed projects easy to distinguish from your other folders. They appear in the dashboard and the Claude app without the underscore (`project1`, `project2`, `clientwork`).

Each project folder can hold any number of repos — clawdstacc always launches `claude` with the project folder as the working directory.

## 5. Install and configure

Use Homebrew (recommended):

```bash
brew tap larskghf/tap
brew install clawdstacc
```

The first `clawdstacc setup` run bootstraps `~/.config/clawdstacc/clawdstacc.conf` from the bundled example and exits — review it before re-running:

```bash
clawdstacc setup
$EDITOR ~/.config/clawdstacc/clawdstacc.conf
```

If you'd rather build from source:

```bash
git clone https://github.com/larskghf/clawdstacc.git ~/clawdstacc
cd ~/clawdstacc
go build -o bin/clawdstacc ./cmd/clawdstacc
```

The same auto-bootstrap kicks in — `clawdstacc setup` from inside the repo finds the `clawdstacc.conf.example` next to the binary.

Most important values in `clawdstacc.conf`:

| Variable | Default | Purpose |
|---|---|---|
| `PROJECTS_GLOB` | `$HOME/_*` | Where your project folders are |
| `EXPLICIT_PROJECTS` | unset | Explicit project list (overrides the glob) |
| `CODESERVER_BIND` | `0.0.0.0:8443` | Where code-server listens |
| `CODESERVER_PASSWORD` | `CHANGE_ME` | **Change this.** `openssl rand -base64 24` |
| `DASHBOARD_PORT` | `8390` | Status dashboard port |
| `CLAUDE_CONTINUE` | `true` | Use `--continue` on auto-start |
| `BREW_PREFIX` | `/opt/homebrew` | Apple Silicon. Intel: `/usr/local` |

## 6. Run setup

```bash
clawdstacc setup
```

The script:
- checks for required tools and aborts on missing ones
- generates a launchd plist per project (`~/Library/LaunchAgents/com.user.clawdstacc.<name>.plist`)
- generates `.vscode/tasks.json` and `.vscode/settings.json` per project
- generates the code-server and dashboard plists
- removes orphan plists (renamed-away or deleted projects)
- loads everything into launchd

It is idempotent — safe to run again whenever you add/remove projects or change config.

## 7. Verify

```bash
clawdstacc status
```

You should see: code-server running, dashboard running, every project with `tmux ●` and `agent ●` green.

In the browser:
- code-server: `http://<host>:8443`
- Dashboard: `http://<host>:8390`

In the Claude app: Code tab. Sessions should appear as `<hostname>-<projectname>-<random>`, each with a green status dot.

## 8. First time in code-server

Open code-server in the browser, enter the password. The first time you open a project (`File → Open Folder → ~/_project1`):

1. VSCode asks "Do you trust the authors of the files?" → Yes
2. VSCode asks "Allow automatic tasks?" → Allow Once or Always Allow
3. The terminal panel opens automatically and attaches to the running tmux session

From then on, opening the project is one click — no prompts.

## 9. Auto-login on the host

So everything comes back automatically after a power outage, the host needs to log in by itself:

System Settings → Users & Groups → Automatic login → your user.

Also, under "General → Login Items", check that nothing blocks login. **FileVault must be disabled** — otherwise the host can't boot unattended (it would wait for a password at boot).

## 10. Add new projects

Create a new folder with a leading underscore. The dashboard auto-discovers it within ~2 seconds and shows a "Set up project" button on the new card — one click and the launchd agent is registered.

If you prefer the CLI:

```bash
mkdir ~/_newproject
clawdstacc setup
```

Setup is idempotent. Existing sessions are reloaded briefly, the new project shows up everywhere.

## Done

That's it. The host runs 24/7, every session is persistent, controllable from the phone and the browser. If anything misbehaves: see `docs/TROUBLESHOOTING.md`.
