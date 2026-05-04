#!/usr/bin/env bash
#
# teardown-stack.sh — entfernt alle generierten launchd-Plists
# und beendet die zugehörigen Prozesse.
# VSCode .vscode/-Ordner und tmux-Sessions bleiben erhalten —
# die kannst du selbst aufräumen falls gewünscht.

set -euo pipefail

LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }

yellow "Das wird folgende launchd-Agents stoppen und entfernen:"
echo
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.claude.*.plist 2>/dev/null || true
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.claude-dashboard.plist 2>/dev/null || true
ls -1 "$LAUNCH_AGENTS_DIR"/com.user.codeserver.plist 2>/dev/null || true
echo
read -r -p "Fortfahren? [y/N] " confirm
[ "$confirm" = "y" ] || [ "$confirm" = "Y" ] || { red "Abgebrochen."; exit 1; }

shopt -s nullglob
for plist in "$LAUNCH_AGENTS_DIR"/com.user.claude.*.plist \
             "$LAUNCH_AGENTS_DIR"/com.user.claude-dashboard.plist \
             "$LAUNCH_AGENTS_DIR"/com.user.codeserver.plist; do
  label="$(basename "$plist" .plist)"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || \
    launchctl unload "$plist" 2>/dev/null || true
  rm -f "$plist"
  green "  entfernt: $(basename "$plist")"
done

echo
yellow "Optional: tmux-Sessions beenden mit:"
echo "  tmux kill-server"
yellow "Oder einzeln:"
echo "  tmux ls"
echo "  tmux kill-session -t <name>"
echo
yellow "VSCode .vscode/-Configs in den Projekten wurden NICHT entfernt."
yellow "Falls gewünscht: rm -rf <projektordner>/.vscode"
