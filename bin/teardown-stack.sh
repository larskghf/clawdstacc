#!/usr/bin/env bash
#
# teardown-stack.sh — remove all generated launchd plists and stop
# their processes. The .vscode/ folders inside projects and any tmux
# sessions are left in place; clean them up yourself if you want to.

set -euo pipefail

LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }

yellow "This will stop and remove the following launchd agents:"
echo
# Current naming
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.claude-stack.*.plist 2>/dev/null || true
# Pre-rename naming (in case anything is still around)
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.claude.*.plist 2>/dev/null || true
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.claude-dashboard.plist 2>/dev/null || true
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.codeserver.plist 2>/dev/null || true
echo
read -r -p "Continue? [y/N] " confirm
[ "$confirm" = "y" ] || [ "$confirm" = "Y" ] || { red "Aborted."; exit 1; }

shopt -s nullglob
for plist in "$LAUNCH_AGENTS_DIR"/com.user.claude-stack.*.plist \
             "$LAUNCH_AGENTS_DIR"/com.user.claude.*.plist \
             "$LAUNCH_AGENTS_DIR"/com.user.claude-dashboard.plist \
             "$LAUNCH_AGENTS_DIR"/com.user.codeserver.plist; do
  label="$(basename "$plist" .plist)"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || \
    launchctl unload "$plist" 2>/dev/null || true
  rm -f "$plist"
  green "  removed: $(basename "$plist")"
done

echo
yellow "Optional: kill tmux sessions with:"
echo "  tmux kill-server"
yellow "Or per-session:"
echo "  tmux ls"
echo "  tmux kill-session -t <name>"
echo
yellow "Note: .vscode/ folders inside your projects were NOT removed."
yellow "If you want them gone: rm -rf <project>/.vscode"
