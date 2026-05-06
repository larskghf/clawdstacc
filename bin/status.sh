#!/usr/bin/env bash
#
# status.sh — quick CLI overview of all stack components.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF_FILE="$REPO_DIR/clawdstacc.conf"

[ -f "$CONF_FILE" ] && source "$CONF_FILE"
: "${PROJECTS_GLOB:=$HOME/_*}"
: "${LOG_DIR:=$HOME/Library/Logs/clawdstacc}"
: "${CODESERVER_BIND:=0.0.0.0:8443}"
: "${DASHBOARD_PORT:=8390}"

bold()   { printf '\033[1m%s\033[0m' "$*"; }
green()  { printf '\033[32m%s\033[0m' "$*"; }
red()    { printf '\033[31m%s\033[0m' "$*"; }
yellow() { printf '\033[33m%s\033[0m' "$*"; }
gray()   { printf '\033[90m%s\033[0m' "$*"; }

# Is a launchd agent loaded?
agent_loaded() {
  # No -q on grep so launchctl can finish writing — otherwise SIGPIPE +
  # pipefail produces false negatives for early-matching labels.
  launchctl list | grep -E "^[^[:space:]]*[[:space:]][^[:space:]]*[[:space:]]$1$" >/dev/null
}

# PID of a launchd agent (or empty)
agent_pid() {
  launchctl list | awk -v lbl="$1" '$3 == lbl { print $1 }' | head -1
}

# Does a tmux session exist?
tmux_exists() {
  tmux has-session -t "$1" 2>/dev/null
}

# Is there a claude process running in the named tmux session?
# Checks the pane pid itself (current plist runs claude directly) and any
# descendants (older shell-wrapped panes had claude as a child of zsh).
claude_in_session() {
  local name="$1"
  local pane_pid
  pane_pid="$(tmux display-message -p -t "$name" '#{pane_pid}' 2>/dev/null)"
  [ -n "$pane_pid" ] || return 1
  # Pane pid itself
  if ps -p "$pane_pid" -o command= 2>/dev/null | grep -qi "claude"; then
    return 0
  fi
  # Descendants
  local children
  children="$(pgrep -P "$pane_pid" 2>/dev/null || true)"
  for child in $children; do
    if ps -p "$child" -o command= 2>/dev/null | grep -qi "claude"; then
      return 0
    fi
  done
  return 1
}

echo
bold "╔══════════════════════════════════════════════════════════════╗"; echo
bold "║                  clawdstacc status                         ║"; echo
bold "╚══════════════════════════════════════════════════════════════╝"; echo
echo

# code-server
printf "%-30s " "code-server"
if agent_loaded "com.user.clawdstacc.codeserver"; then
  pid="$(agent_pid com.user.clawdstacc.codeserver)"
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
if agent_loaded "com.user.clawdstacc.dashboard"; then
  pid="$(agent_pid com.user.clawdstacc.dashboard)"
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

bold "Projects"; echo
gray "──────────────────────────────────────────────────────────────"; echo

if declare -p EXPLICIT_PROJECTS &>/dev/null && [ "${#EXPLICIT_PROJECTS[@]}" -gt 0 ]; then
  projects=("${EXPLICIT_PROJECTS[@]}")
else
  shopt -s nullglob
  # shellcheck disable=SC2206
  projects=( $PROJECTS_GLOB )
  shopt -u nullglob
fi

if [ "${#projects[@]}" -eq 0 ]; then
  yellow "  No projects found (PROJECTS_GLOB=$PROJECTS_GLOB)"
else
  for project_path in "${projects[@]}"; do
    [ -d "$project_path" ] || continue
    name="$(basename "$project_path")"
    name="${name#_}"
    label="com.user.clawdstacc.$name"

    printf "  %-26s " "$name"

    # tmux status
    if tmux_exists "$name"; then
      printf "%s tmux  " "$(green ●)"
    else
      printf "%s tmux  " "$(red ●)"
    fi

    # claude status
    if tmux_exists "$name" && claude_in_session "$name"; then
      printf "%s claude  " "$(green ●)"
    else
      printf "%s claude  " "$(red ●)"
    fi

    # launchd status
    if agent_loaded "$label"; then
      printf "%s agent " "$(green ●)"
    else
      printf "%s agent " "$(red ●)"
    fi

    # Last session activity
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
          gray "  last activity: ${ago}s"
        elif [ "$ago" -lt 3600 ]; then
          gray "  last activity: $((ago/60))m"
        elif [ "$ago" -lt 86400 ]; then
          gray "  last activity: $((ago/3600))h"
        else
          gray "  last activity: $((ago/86400))d"
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
    yellow "  ⚠ non-empty .err files:"
    echo
    for f in $recent_errors; do
      echo "    $f ($(wc -l < "$f") lines)"
    done
  else
    green "  ✓ no errors in logs"; echo
  fi
fi
echo
