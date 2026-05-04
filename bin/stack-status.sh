#!/usr/bin/env bash
#
# stack-status.sh — schnelle CLI-Übersicht über alle Stack-Komponenten.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF_FILE="$REPO_DIR/stack.conf"

[ -f "$CONF_FILE" ] && source "$CONF_FILE"
: "${PROJECTS_GLOB:=$HOME/_*}"
: "${LOG_DIR:=$HOME/Library/Logs/claude-stack}"
: "${CODESERVER_BIND:=0.0.0.0:8443}"
: "${DASHBOARD_PORT:=8390}"

bold()   { printf '\033[1m%s\033[0m' "$*"; }
green()  { printf '\033[32m%s\033[0m' "$*"; }
red()    { printf '\033[31m%s\033[0m' "$*"; }
yellow() { printf '\033[33m%s\033[0m' "$*"; }
gray()   { printf '\033[90m%s\033[0m' "$*"; }

# Ist ein launchd-Agent geladen?
agent_loaded() {
  launchctl list | grep -q "^[^[:space:]]*[[:space:]][^[:space:]]*[[:space:]]$1$"
}

# PID eines launchd-Agents (oder leer)
agent_pid() {
  launchctl list | awk -v lbl="$1" '$3 == lbl { print $1 }' | head -1
}

# Existiert eine tmux-Session?
tmux_exists() {
  tmux has-session -t "$1" 2>/dev/null
}

echo
bold "╔══════════════════════════════════════════════════════════════╗"; echo
bold "║                  claude-stack status                         ║"; echo
bold "╚══════════════════════════════════════════════════════════════╝"; echo
echo

# code-server
printf "%-30s " "code-server"
if agent_loaded "com.user.codeserver"; then
  pid="$(agent_pid com.user.codeserver)"
  if [ -n "$pid" ] && [ "$pid" != "-" ]; then
    green "● running"; printf "  (pid %s, %s)" "$pid" "$CODESERVER_BIND"
  else
    yellow "● loaded but not running"
  fi
else
  red "● not loaded"
fi
echo

# Dashboard
printf "%-30s " "dashboard"
if agent_loaded "com.user.claude-dashboard"; then
  pid="$(agent_pid com.user.claude-dashboard)"
  if [ -n "$pid" ] && [ "$pid" != "-" ]; then
    green "● running"; printf "  (pid %s, port %s)" "$pid" "$DASHBOARD_PORT"
  else
    yellow "● loaded but not running"
  fi
else
  red "● not loaded"
fi
echo
echo

bold "Projekte"; echo
gray "──────────────────────────────────────────────────────────────"; echo

shopt -s nullglob
projects=( $PROJECTS_GLOB )
shopt -u nullglob

if [ "${#projects[@]}" -eq 0 ]; then
  yellow "  Keine Projekte gefunden (PROJECTS_GLOB=$PROJECTS_GLOB)"
else
  for project_path in "${projects[@]}"; do
    [ -d "$project_path" ] || continue
    name="$(basename "$project_path")"
    name="${name#_}"
    label="com.user.claude.$name"

    printf "  %-26s " "$name"

    # tmux Status
    if tmux_exists "$name"; then
      printf "%s tmux  " "$(green ●)"
    else
      printf "%s tmux  " "$(red ●)"
    fi

    # launchd Status
    if agent_loaded "$label"; then
      printf "%s agent " "$(green ●)"
    else
      printf "%s agent " "$(red ●)"
    fi

    # Letzte Session-Aktivität
    sanitized="$(echo "$project_path" | sed 's|/|-|g' | sed 's|^-||')"
    sessions_dir="$HOME/.claude/projects/-${sanitized}"
    if [ -d "$sessions_dir" ]; then
      latest_jsonl="$(ls -t "$sessions_dir"/*.jsonl 2>/dev/null | head -1)"
      if [ -n "$latest_jsonl" ]; then
        # macOS stat
        mtime=$(stat -f %m "$latest_jsonl")
        now=$(date +%s)
        ago=$((now - mtime))
        if [ "$ago" -lt 60 ]; then
          gray "  letzte Aktivität: ${ago}s"
        elif [ "$ago" -lt 3600 ]; then
          gray "  letzte Aktivität: $((ago/60))m"
        elif [ "$ago" -lt 86400 ]; then
          gray "  letzte Aktivität: $((ago/3600))h"
        else
          gray "  letzte Aktivität: $((ago/86400))d"
        fi
      fi
    fi

    echo
  done
fi

echo
bold "Logs"; echo
gray "──────────────────────────────────────────────────────────────"; echo
echo "  $LOG_DIR"
if [ -d "$LOG_DIR" ]; then
  recent_errors="$(find "$LOG_DIR" -name "*.err" -size +0c 2>/dev/null | head -5)"
  if [ -n "$recent_errors" ]; then
    yellow "  ⚠ Es gibt nicht-leere .err-Dateien:"
    echo
    for f in $recent_errors; do
      echo "    $f ($(wc -l < "$f") Zeilen)"
    done
  else
    green "  ✓ keine Fehler in den Logs"; echo
  fi
fi
echo
