#!/usr/bin/env bash
#
# setup-stack.sh — claude-stack setup
#
# Reads stack.conf, scans the project folders, and generates a launchd plist
# and .vscode config per project, plus single plists for code-server and the
# dashboard. Loads everything into launchd.
#
# Idempotent: safe to run any number of times.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATES_DIR="$REPO_DIR/templates"
CONF_FILE="$REPO_DIR/stack.conf"
LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"

# --- Helpers ---

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
blue()   { printf '\033[34m%s\033[0m\n' "$*"; }

die() { red "ERROR: $*"; exit 1; }

# Replace __KEY__ placeholders in a file (in-place).
# Usage: render_template <src> <dst> KEY1=val1 KEY2=val2 ...
render_template() {
  local src="$1" dst="$2"; shift 2
  local content
  content="$(cat "$src")"
  while [ $# -gt 0 ]; do
    local kv="$1"; shift
    local key="${kv%%=*}" val="${kv#*=}"
    content="${content//__${key}__/${val}}"
  done
  printf '%s' "$content" > "$dst"
}

# --- Load config ---

[ -f "$CONF_FILE" ] || die "stack.conf not found. Copy stack.conf.example to stack.conf and edit it."

# shellcheck source=/dev/null
source "$CONF_FILE"

# Defaults if not set in conf
: "${PROJECTS_GLOB:=$HOME/_*}"
: "${CODESERVER_BIND:=0.0.0.0:8443}"
: "${CODESERVER_PASSWORD:=CHANGE_ME}"
: "${DASHBOARD_PORT:=8390}"
: "${LOG_DIR:=$HOME/Library/Logs/claude-stack}"
: "${CLAUDE_CONTINUE:=true}"
: "${CLAUDE_EXTRA_FLAGS:=}"
: "${BREW_PREFIX:=/opt/homebrew}"

# --- Pre-flight checks ---

blue "==> Pre-flight checks"

[ "$(uname)" = "Darwin" ] || die "This setup targets macOS. You are on $(uname)."

[ -x "$BREW_PREFIX/bin/brew" ] || die "Homebrew not found at $BREW_PREFIX. Adjust BREW_PREFIX in stack.conf or install brew."

for cmd in tmux code-server; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    yellow "  missing: $cmd  (brew install $cmd)"
    MISSING=1
  fi
done

# python3 — the plist needs an absolute path. Try the brew symlink, a
# brew-versioned binary, then system Python (in that order).
PYTHON3_BIN=""
for candidate in \
    "$BREW_PREFIX/bin/python3" \
    "$BREW_PREFIX/bin/python3.13" \
    "$BREW_PREFIX/bin/python3.12" \
    "$BREW_PREFIX/bin/python3.11" \
    "/usr/bin/python3"; do
  if [ -x "$candidate" ]; then
    PYTHON3_BIN="$candidate"
    break
  fi
done
if [ -z "$PYTHON3_BIN" ]; then
  yellow "  missing: python3  (brew install python@3.12)"
  MISSING=1
fi

if ! command -v claude >/dev/null 2>&1; then
  yellow "  missing: claude  (see https://claude.com/code)"
  MISSING=1
fi

[ -z "${MISSING:-}" ] || die "Install missing tools and re-run."

if [ "$CODESERVER_PASSWORD" = "CHANGE_ME" ]; then
  yellow "WARNING: CODESERVER_PASSWORD is still 'CHANGE_ME'. Generate one with 'openssl rand -base64 24' and set it in stack.conf."
fi

green "  ✓ macOS, brew, tmux, code-server, python3 ($PYTHON3_BIN), claude"

# --- Create directories ---

mkdir -p "$LAUNCH_AGENTS_DIR" "$LOG_DIR"

# --- Resolve projects ---

PROJECTS=()
if declare -p EXPLICIT_PROJECTS &>/dev/null && [ "${#EXPLICIT_PROJECTS[@]}" -gt 0 ]; then
  PROJECTS=("${EXPLICIT_PROJECTS[@]}")
else
  shopt -s nullglob
  # shellcheck disable=SC2206
  PROJECTS=( $PROJECTS_GLOB )
  shopt -u nullglob
fi

[ "${#PROJECTS[@]}" -gt 0 ] || die "No projects found. PROJECTS_GLOB='$PROJECTS_GLOB' matched nothing."

blue "==> Projects"
for p in "${PROJECTS[@]}"; do
  [ -d "$p" ] || { yellow "  skipped (not a directory): $p"; continue; }
  green "  • $(basename "$p")  ($p)"
done

# --- Per-project: plist + VSCode config ---

blue "==> Generating per-project configuration"

GENERATED_PROJECTS=()

for project_path in "${PROJECTS[@]}"; do
  [ -d "$project_path" ] || continue

  # Name = directory name minus a leading underscore
  raw_name="$(basename "$project_path")"
  project_name="${raw_name#_}"
  GENERATED_PROJECTS+=("$project_name")

  # 1) launchd plist for tmux+claude
  plist_path="$LAUNCH_AGENTS_DIR/com.user.claude-stack.${project_name}.plist"
  render_template "$TEMPLATES_DIR/claude.plist.tmpl" "$plist_path" \
    "PROJECT_NAME=$project_name" \
    "PROJECT_PATH=$project_path" \
    "USER_HOME=$HOME" \
    "BREW_PREFIX=$BREW_PREFIX" \
    "LOG_DIR=$LOG_DIR" \
    "CLAUDE_CONTINUE=$CLAUDE_CONTINUE" \
    "CLAUDE_EXTRA_FLAGS=$CLAUDE_EXTRA_FLAGS"

  # 2) VSCode tasks.json + settings.json
  vscode_dir="$project_path/.vscode"
  mkdir -p "$vscode_dir"

  # Only overwrite if the file is missing or recognizably ours (marker string).
  if [ ! -f "$vscode_dir/tasks.json" ] || grep -q "claude --rc" "$vscode_dir/tasks.json" 2>/dev/null; then
    render_template "$TEMPLATES_DIR/vscode-tasks.json.tmpl" "$vscode_dir/tasks.json" \
      "PROJECT_NAME=$project_name"
  else
    yellow "  skipped (exists, not ours): $vscode_dir/tasks.json"
  fi

  if [ ! -f "$vscode_dir/settings.json" ] || grep -q "tmux-claude" "$vscode_dir/settings.json" 2>/dev/null; then
    render_template "$TEMPLATES_DIR/vscode-settings.json.tmpl" "$vscode_dir/settings.json" \
      "PROJECT_NAME=$project_name"
  else
    yellow "  skipped (exists, not ours): $vscode_dir/settings.json"
  fi

  green "  ✓ $project_name"
done

# --- code-server plist ---

blue "==> Generating code-server plist"
render_template "$TEMPLATES_DIR/codeserver.plist.tmpl" "$LAUNCH_AGENTS_DIR/com.user.claude-stack.codeserver.plist" \
  "BREW_PREFIX=$BREW_PREFIX" \
  "USER_HOME=$HOME" \
  "LOG_DIR=$LOG_DIR" \
  "CODESERVER_BIND=$CODESERVER_BIND" \
  "CODESERVER_PASSWORD=$CODESERVER_PASSWORD"
green "  ✓ codeserver.plist"

# --- dashboard plist ---

blue "==> Generating dashboard plist"
render_template "$TEMPLATES_DIR/dashboard.plist.tmpl" "$LAUNCH_AGENTS_DIR/com.user.claude-stack.dashboard.plist" \
  "BREW_PREFIX=$BREW_PREFIX" \
  "PYTHON3_BIN=$PYTHON3_BIN" \
  "USER_HOME=$HOME" \
  "LOG_DIR=$LOG_DIR" \
  "REPO_PATH=$REPO_DIR" \
  "DASHBOARD_PORT=$DASHBOARD_PORT"
green "  ✓ dashboard.plist"

# --- launchd reload ---

blue "==> launchd: unload old agents, load new ones"

# Plists we just generated (the desired state).
ALL_PLISTS=()
for project_name in "${GENERATED_PROJECTS[@]}"; do
  ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.claude-stack.${project_name}.plist")
done
ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.claude-stack.codeserver.plist")
ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.claude-stack.dashboard.plist")

# Clean up orphans — pre-rename labels (com.user.claude.*, com.user.codeserver,
# com.user.claude-dashboard) and current-naming plists not in the config anymore.
shopt -s nullglob
ORPHANS=()
for old in "$LAUNCH_AGENTS_DIR"/com.user.claude.*.plist \
           "$LAUNCH_AGENTS_DIR"/com.user.codeserver.plist \
           "$LAUNCH_AGENTS_DIR"/com.user.claude-dashboard.plist; do
  ORPHANS+=("$old")
done
for existing in "$LAUNCH_AGENTS_DIR"/com.user.claude-stack.*.plist; do
  keep=0
  for wanted in "${ALL_PLISTS[@]}"; do
    [ "$existing" = "$wanted" ] && { keep=1; break; }
  done
  [ "$keep" = "0" ] && ORPHANS+=("$existing")
done
shopt -u nullglob

for plist in "${ORPHANS[@]}"; do
  label="$(basename "$plist" .plist)"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  rm -f "$plist"
  yellow "  ✗ orphan removed: $label"
done

for plist in "${ALL_PLISTS[@]}"; do
  label="$(basename "$plist" .plist)"
  # Boot out first (no-op if not loaded), then load fresh.
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
done

for plist in "${ALL_PLISTS[@]}"; do
  loaded=0
  # Retry: a port may stay in TIME_WAIT briefly after bootout.
  for attempt in 1 2 3; do
    if launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null; then
      loaded=1; break
    fi
    [ "$attempt" -lt 3 ] && sleep 1
  done
  if [ "$loaded" = "1" ]; then
    green "  ✓ loaded: $(basename "$plist")"
  elif launchctl load "$plist" 2>/dev/null; then
    green "  ✓ loaded (legacy): $(basename "$plist")"
  else
    red "  ✗ failed to load: $plist"
  fi
done

# --- Done ---

echo
green "═══════════════════════════════════════════════════════════════"
green "  Setup complete."
green "═══════════════════════════════════════════════════════════════"
echo
echo "Status:        ./bin/stack-status.sh"
echo "code-server:   http://<host>:${CODESERVER_BIND##*:}"
echo "Dashboard:     http://<host>:${DASHBOARD_PORT}"
echo "Logs:          tail -f ${LOG_DIR}/*.log"
echo
echo "On your phone: Claude app → Code tab. Sessions should appear within seconds."
echo
yellow "Note: when you first open a project in code-server, VSCode will ask"
yellow "whether to allow automatic tasks. Confirm to enable the auto-attach."
