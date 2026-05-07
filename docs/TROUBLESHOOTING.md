# Troubleshooting

## "claude: command not found" during setup

`clawdstacc setup` checks that `claude` is on your PATH. If the native installer didn't add the path to your shell config, add it manually:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

The plist itself adds `~/.local/bin` to its own PATH, so the launchd-managed processes will find claude even without the shell-config change.

## Sessions don't appear in the Claude app

First checks:

```bash
clawdstacc status
```

- `tmux ●` red? → no tmux session. Look at the logs: `tail ~/Library/Logs/clawdstacc/claude-<name>.err`
- `agent ●` red? → launchd hasn't loaded the plist. `launchctl list | grep clawdstacc` shows what is actually loaded.
- tmux ✓ but no claude in the dashboard? → tmux is up but claude crashed or hasn't started. `tmux attach -t <name>` and look at the pane.

In the Claude app: is the right account logged in? Remote Control needs a Pro or Max subscription. Free accounts don't see sessions.

## Terminal in code-server has no theme / no plugins / commands missing

The classic "launchd env vs interactive shell" issue. launchd starts code-server with a minimal environment, and the integrated terminal inherits it.

**The default setup should already handle this:** the generated `vscode-settings.json` sets `zsh-login` (with `-l -i`) as the default profile. If that's not taking effect, check:

1. **Settings overridden?** In code-server, open `Cmd+,` and search for the default terminal profile (osx). It should be `zsh-login`. If not, the user-level settings override the workspace setting — set the same value at user level.

2. **`.zshrc` not loading?** In a code-server terminal:
   ```bash
   echo $0           # should be "-zsh" (the leading dash means login shell)
   echo $ZSH         # should show your oh-my-zsh path
   echo $ZSH_THEME   # your theme
   ```
   If `$0` has no leading dash → not a login shell. Check the profile args; `-l` must be in there.

3. **Error in `.zshrc`?** Test manually:
   ```bash
   /bin/zsh -l -i -c 'echo OK'
   ```
   Errors will show up here.

4. **Brew paths missing?** Apple Silicon has a different default PATH. Make sure this line is in `~/.zprofile` or `~/.zshrc`:
   ```bash
   eval "$(/opt/homebrew/bin/brew shellenv)"
   ```

## Terminal in tmux session has no theme / no plugins

The pane is currently running Claude — to get to a shell with your full env, open a new window with Ctrl+b c. That uses `default-command` from clawdstacc's embedded tmux config (which sets `${SHELL} -l` so your zsh setup loads). If you upgraded from a pre-tmux-socket clawdstacc and your sessions still live on the default tmux socket, you may see the system shell instead of your login shell — finish the migration:

```bash
# Re-run setup with the new binary (reads new plists, bootstraps on the dedicated socket)
clawdstacc setup --conf ~/clawdstacc.conf

# If you're attached to one of the project sessions, /exit Claude to let
# launchd respawn it under the new socket, or kill manually:
tmux kill-session -t <project>
```

Inspect what got installed: `cat ~/.config/clawdstacc/tmux.conf`.

Note: when Claude exits the pane closes and the session ends — by design, so launchd respawns a fresh Claude instance immediately. There is no "shell after Claude" fallback in the tmux pane anymore.

## "Unable to connect to claude.ai" or network timeouts

Claude Code Remote Control needs internet continuously. If the host loses connectivity for ~10 minutes the process exits. launchd respawns it automatically, and `--continue` keeps the conversation going. If it happens often: check WiFi stability, or wire the host.

## VSCode doesn't open a terminal on folder open

Three likely causes:

1. **Auto-tasks disabled.** Open Settings (`Cmd+,`) and search for `task.allowAutomaticTasks`; set to `on`. Our `settings.json` already sets this, but user-level settings can override it.

2. **Workspace not trusted.** code-server requires the workspace to be trusted before auto-tasks run. The first time you open a folder, click "Trust" on the prompt.

3. **`runOn: folderOpen` not firing.** Sometimes broken after VSCode/code-server updates. Workaround: run `Tasks: Run Task → Attach Claude (tmux)` from the command palette manually.

The task itself does `tmux attach || tmux new`, so even if attaching fails it falls back to creating a session.

## `claude --continue` finds no session

On the first start there is no session to continue. Setup uses a marker file (`<project>/.claude/.has-session`) which is written after the first successful start.

If it ever drifts out of sync (e.g. you cleaned up `~/.claude/projects/` manually):

```bash
rm ~/_<project>/.claude/.has-session
launchctl kickstart -k "gui/$(id -u)/com.user.clawdstacc.<project>"
```

## Panel position in code-server doesn't change after editing settings

`workbench.panel.defaultLocation` is the **default for new workspaces**. Once VSCode has remembered a position for an existing workspace, the default is ignored. Two ways to deal with it:

1. **Move it manually** (one-time): right-click the panel title bar → "Move Panel Right".
2. **Reset workspace state**: close the workspace, then delete the matching directory in `~/.local/share/code-server/User/workspaceStorage/`. You lose the persisted UI state (tab layout, search history) but no files or settings.

## launchd respawns the process in a tight loop

Logs:

```bash
log show --predicate 'subsystem == "com.apple.xpc.launchd"' --last 10m | grep clawdstacc
```

Most common cause: a config error in the plist or a missing binary on PATH. Check `~/Library/Logs/clawdstacc/claude-<name>.err`. The plist sets `ThrottleInterval: 10`, so the worst case is one respawn per 10 seconds.

If you want to stop a session deliberately:

```bash
launchctl bootout "gui/$(id -u)/com.user.clawdstacc.<project>"
tmux kill-session -t <project>
```

To bring it back:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.user.clawdstacc.<project>.plist
```

## Dashboard says "agent: no agent" while `launchctl list` shows it

The dashboard parses `launchctl list` with a regex. On very old macOS the format may differ slightly. If `tmux ●` and `claude ●` are green, you're fine — the agent badge is secondary. If you want to fix it, adjust `agentLoaded()` in `status.go`.

## code-server "Connection lost" after idle

By default code-server holds WebSocket connections open. If you're going through a reverse proxy, it may drop the connection after a long idle period. Just close the browser tab and reopen — the backend session keeps running and you land in the same state.

## Closed the code-server tab — is my session gone?

No. If you ran `tmux attach` in the integrated terminal and close the browser tab, the tmux session continues to run. Next time you open the browser, `runOn: folderOpen` re-attaches automatically. If auto-attach fails for some reason, run `tmux attach -t <project>` manually in any terminal.

## Permissions / "Operation not permitted"

macOS requires explicit permission for many operations. If you see permission errors in the logs (e.g. claude can't read files):

System Settings → Privacy & Security → "Full Disk Access" → add the terminal you ran setup from (Terminal.app, iTerm2, etc.). Reboot.

In some cases adding `bash` itself to the same list helps too.

## Reset the stack from scratch

If the configuration feels tangled:

```bash
clawdstacc teardown    # remove plists and stop processes
clawdstacc setup       # regenerate everything
```

Safe — your `~/.claude/projects/` conversation history is untouched, no data loss.

## Tail every log at once

```bash
tail -f ~/Library/Logs/clawdstacc/*.log ~/Library/Logs/clawdstacc/*.err
```

Or with `multitail` for a side-by-side view:

```bash
brew install multitail
multitail ~/Library/Logs/clawdstacc/claude-*.log
```
