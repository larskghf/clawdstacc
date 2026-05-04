#!/usr/bin/env bash
#
# setup-stack.sh — claude-stack Setup
#
# Liest stack.conf, scannt Projektordner, generiert pro Projekt eine
# launchd-Plist und eine .vscode-Config, sowie eine Plist für code-server
# und das Dashboard. Lädt alles in launchd.
#
# Idempotent: kann beliebig oft aufgerufen werden.

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

die() { red "FEHLER: $*"; exit 1; }

# Ersetzt __KEY__ Platzhalter in einer Datei (in-place)
# Nutzung: render_template <src> <dst> KEY1=val1 KEY2=val2 ...
render_template() {
  local src="$1" dst="$2"; shift 2
  local content
  content="$(cat "$src")"
  while [ $# -gt 0 ]; do
    local kv="$1"; shift
    local key="${kv%%=*}" val="${kv#*=}"
    # bash parameter expansion mit / trennzeichen — escape nötig fuer pfade
    content="${content//__${key}__/${val}}"
  done
  printf '%s' "$content" > "$dst"
}

# --- Config laden ---

[ -f "$CONF_FILE" ] || die "stack.conf nicht gefunden. Kopiere stack.conf.example nach stack.conf und passe sie an."

# shellcheck source=/dev/null
source "$CONF_FILE"

# Defaults setzen falls nicht in conf
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

[ "$(uname)" = "Darwin" ] || die "Dieses Setup ist für macOS. Du läufst auf $(uname)."

[ -x "$BREW_PREFIX/bin/brew" ] || die "Homebrew nicht unter $BREW_PREFIX gefunden. Pfad in stack.conf anpassen oder brew installieren."

for cmd in tmux code-server python3; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    yellow "  fehlt: $cmd  (brew install $cmd)"
    MISSING=1
  fi
done

if ! command -v claude >/dev/null 2>&1; then
  yellow "  fehlt: claude  (siehe https://claude.com/code für die Installation)"
  MISSING=1
fi

[ -z "${MISSING:-}" ] || die "Bitte fehlende Tools installieren und erneut ausführen."

if [ "$CODESERVER_PASSWORD" = "CHANGE_ME" ]; then
  yellow "WARNUNG: CODESERVER_PASSWORD steht noch auf 'CHANGE_ME'. Generiere ein eigenes mit 'openssl rand -base64 24' und setze es in stack.conf."
fi

green "  ✓ macOS, brew, tmux, code-server, python3, claude"

# --- Verzeichnisse anlegen ---

mkdir -p "$LAUNCH_AGENTS_DIR" "$LOG_DIR"

# --- Projekte ermitteln ---

PROJECTS=()
if declare -p EXPLICIT_PROJECTS &>/dev/null && [ "${#EXPLICIT_PROJECTS[@]}" -gt 0 ]; then
  PROJECTS=("${EXPLICIT_PROJECTS[@]}")
else
  # Glob expandieren
  shopt -s nullglob
  # shellcheck disable=SC2206
  PROJECTS=( $PROJECTS_GLOB )
  shopt -u nullglob
fi

[ "${#PROJECTS[@]}" -gt 0 ] || die "Keine Projekte gefunden. PROJECTS_GLOB='$PROJECTS_GLOB' liefert nichts."

blue "==> Gefundene Projekte"
for p in "${PROJECTS[@]}"; do
  [ -d "$p" ] || { yellow "  übersprungen (kein Verzeichnis): $p"; continue; }
  green "  • $(basename "$p")  ($p)"
done

# --- Per-Projekt: Plist + VSCode-Config generieren ---

blue "==> Generiere Per-Projekt Konfiguration"

# Sammle die Project-Namen für später (Dashboard-State, Cleanup)
GENERATED_PROJECTS=()

for project_path in "${PROJECTS[@]}"; do
  [ -d "$project_path" ] || continue

  # Name = Verzeichnisname ohne führendes Underscore
  raw_name="$(basename "$project_path")"
  project_name="${raw_name#_}"
  GENERATED_PROJECTS+=("$project_name")

  # 1) launchd plist für tmux+claude
  plist_path="$LAUNCH_AGENTS_DIR/com.user.claude.${project_name}.plist"
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

  # Nur überschreiben wenn nicht existiert oder von uns generiert (Marker-Kommentar prüfen)
  if [ ! -f "$vscode_dir/tasks.json" ] || grep -q "claude --rc" "$vscode_dir/tasks.json" 2>/dev/null; then
    render_template "$TEMPLATES_DIR/vscode-tasks.json.tmpl" "$vscode_dir/tasks.json" \
      "PROJECT_NAME=$project_name"
  else
    yellow "  übersprungen (existiert bereits, nicht von uns): $vscode_dir/tasks.json"
  fi

  if [ ! -f "$vscode_dir/settings.json" ] || grep -q "tmux-claude" "$vscode_dir/settings.json" 2>/dev/null; then
    render_template "$TEMPLATES_DIR/vscode-settings.json.tmpl" "$vscode_dir/settings.json" \
      "PROJECT_NAME=$project_name"
  else
    yellow "  übersprungen (existiert bereits, nicht von uns): $vscode_dir/settings.json"
  fi

  green "  ✓ $project_name"
done

# --- code-server plist ---

blue "==> Generiere code-server Plist"
render_template "$TEMPLATES_DIR/codeserver.plist.tmpl" "$LAUNCH_AGENTS_DIR/com.user.codeserver.plist" \
  "BREW_PREFIX=$BREW_PREFIX" \
  "USER_HOME=$HOME" \
  "LOG_DIR=$LOG_DIR" \
  "CODESERVER_BIND=$CODESERVER_BIND" \
  "CODESERVER_PASSWORD=$CODESERVER_PASSWORD"
green "  ✓ codeserver.plist"

# --- dashboard plist ---

blue "==> Generiere Dashboard Plist"
render_template "$TEMPLATES_DIR/dashboard.plist.tmpl" "$LAUNCH_AGENTS_DIR/com.user.claude-dashboard.plist" \
  "BREW_PREFIX=$BREW_PREFIX" \
  "USER_HOME=$HOME" \
  "LOG_DIR=$LOG_DIR" \
  "REPO_PATH=$REPO_DIR" \
  "DASHBOARD_PORT=$DASHBOARD_PORT"
green "  ✓ dashboard.plist"

# --- launchd reload ---

blue "==> launchd: alte Agents entladen, neue laden"

# Alle relevanten Plists durchgehen
ALL_PLISTS=()
for project_name in "${GENERATED_PROJECTS[@]}"; do
  ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.claude.${project_name}.plist")
done
ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.codeserver.plist")
ALL_PLISTS+=("$LAUNCH_AGENTS_DIR/com.user.claude-dashboard.plist")

for plist in "${ALL_PLISTS[@]}"; do
  label="$(basename "$plist" .plist)"
  # Stop & unload — Fehler ignorieren falls noch nicht geladen
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
done

for plist in "${ALL_PLISTS[@]}"; do
  if launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null; then
    green "  ✓ geladen: $(basename "$plist")"
  else
    # Fallback für ältere macOS Versionen
    if launchctl load "$plist" 2>/dev/null; then
      green "  ✓ geladen (legacy): $(basename "$plist")"
    else
      red "  ✗ konnte nicht laden: $plist"
    fi
  fi
done

# --- Done ---

echo
green "═══════════════════════════════════════════════════════════════"
green "  Setup fertig!"
green "═══════════════════════════════════════════════════════════════"
echo
echo "Status checken:           ./bin/stack-status.sh"
echo "code-server:              http://<host>:${CODESERVER_BIND##*:}"
echo "Dashboard:                http://<host>:${DASHBOARD_PORT}"
echo "Logs:                     tail -f ${LOG_DIR}/*.log"
echo
echo "Auf dem Phone: Claude-App → Code-Tab. Du solltest deine Sessions"
echo "in ein paar Sekunden in der Liste sehen."
echo
yellow "Hinweis: Beim ersten Öffnen eines Projekts in code-server fragt"
yellow "VSCode einmal nach, ob automatische Tasks erlaubt sind. Bestätigen."
