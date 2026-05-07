#!/usr/bin/env bash
#
# install.sh — clawdstacc bootstrap
#
# One-liner:
#   bash <(curl -fsSL https://raw.githubusercontent.com/larskghf/clawdstacc/main/install.sh)
#
# What this does:
#   1. Verifies macOS + Homebrew
#   2. Installs missing deps (tmux, code-server, go) via brew if you say yes
#   3. Installs claude CLI if missing (one prompt)
#   4. Clones (or updates) the repo into ~/clawdstacc
#   5. Generates clawdstacc.conf with a fresh CODESERVER_PASSWORD
#   6. Builds the unified Go binary at bin/clawdstacc
#   7. Runs `clawdstacc setup` — registers all launchd agents
#
# Env vars:
#   CLAWDSTACC_HOME      — install dir (default: ~/clawdstacc)
#   CLAWDSTACC_REPO      — git remote (default: github.com/larskghf/clawdstacc)
#   CLAWDSTACC_BRANCH    — branch to check out (default: main)
#   CLAWDSTACC_YES=1     — non-interactive; assume yes for every prompt
#   CLAWDSTACC_HEADLESS=1 — skip code-server (Remote Control sessions only,
#                          no in-browser IDE)

set -euo pipefail

REPO="${CLAWDSTACC_REPO:-https://github.com/larskghf/clawdstacc.git}"
INSTALL_DIR="${CLAWDSTACC_HOME:-$HOME/clawdstacc}"
BRANCH="${CLAWDSTACC_BRANCH:-main}"
ASSUME_YES="${CLAWDSTACC_YES:-}"
HEADLESS="${CLAWDSTACC_HEADLESS:-}"

# --- output helpers ---
red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
blue()   { printf '\033[34m%s\033[0m\n' "$*"; }
bold()   { printf '\033[1m%s\033[0m\n' "$*"; }

die() { red "ERROR: $*"; exit 1; }

# Read y/N from /dev/tty so this works in `curl | bash` (where stdin is the script).
prompt_yn() {
  local q="$1"
  if [ -n "$ASSUME_YES" ]; then
    yellow "  [auto-yes] $q"
    return 0
  fi
  if [ ! -t 0 ] && [ ! -e /dev/tty ]; then
    die "non-interactive shell with no /dev/tty — set CLAWDSTACC_YES=1 to skip prompts"
  fi
  printf '%s [y/N] ' "$q" > /dev/tty
  local ans
  read -r ans < /dev/tty || ans=""
  case "$ans" in y|Y|yes|Yes|YES) return 0 ;; *) return 1 ;; esac
}

# --- preflight ---

bold "==> clawdstacc installer"
echo

[ "$(uname)" = "Darwin" ] || die "macOS only (you are on $(uname)). Linux support is on the roadmap."

[ "$(id -u)" -ne 0 ] || die "don't run me as root"

if ! command -v brew >/dev/null 2>&1; then
  red "Homebrew not found."
  echo "Install it first: https://brew.sh"
  exit 1
fi

# --- dependency check & install ---

required_tools=(tmux go)
[ -z "$HEADLESS" ] && required_tools+=(code-server)

missing=()
for tool in "${required_tools[@]}"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    missing+=("$tool")
  fi
done

if [ ${#missing[@]} -gt 0 ]; then
  yellow "Missing tools: ${missing[*]}"
  if prompt_yn "Install via 'brew install ${missing[*]}'?"; then
    brew install "${missing[@]}"
  else
    die "Install missing tools and re-run."
  fi
else
  if [ -z "$HEADLESS" ]; then
    green "  ✓ tmux, code-server, go"
  else
    green "  ✓ tmux, go  (headless mode — code-server not required)"
  fi
fi

# claude CLI installs from claude.com (not brew)
if ! command -v claude >/dev/null 2>&1; then
  yellow "Claude Code CLI not found."
  if prompt_yn "Run 'curl -fsSL https://claude.com/install.sh | bash' to install it?"; then
    curl -fsSL https://claude.com/install.sh | bash
    # The installer typically lands in ~/.local/bin
    export PATH="$HOME/.local/bin:$PATH"
    if ! command -v claude >/dev/null 2>&1; then
      die "claude still not on PATH after install. Add ~/.local/bin to PATH and re-run."
    fi
  else
    die "Install Claude Code from https://claude.com/code and re-run."
  fi
else
  green "  ✓ claude"
fi

# --- clone or update repo ---

echo
if [ -d "$INSTALL_DIR/.git" ]; then
  blue "==> Updating existing checkout: $INSTALL_DIR"
  git -C "$INSTALL_DIR" fetch --quiet origin "$BRANCH"
  git -C "$INSTALL_DIR" checkout --quiet "$BRANCH"
  git -C "$INSTALL_DIR" pull --ff-only --quiet origin "$BRANCH"
else
  blue "==> Cloning $REPO to $INSTALL_DIR"
  git clone --quiet --branch "$BRANCH" "$REPO" "$INSTALL_DIR"
fi
green "  ✓ $INSTALL_DIR"

cd "$INSTALL_DIR"

# --- generate clawdstacc.conf if missing ---

if [ ! -f clawdstacc.conf ]; then
  blue "==> Creating clawdstacc.conf"
  cp clawdstacc.conf.example clawdstacc.conf
  if [ -z "$HEADLESS" ]; then
    pw="$(openssl rand -base64 24)"
    pw_escaped="${pw//&/\\&}"
    sed -i '' "s|CODESERVER_PASSWORD=\"CHANGE_ME\"|CODESERVER_PASSWORD=\"${pw_escaped}\"|" clawdstacc.conf
    green "  ✓ clawdstacc.conf"
    yellow "  ↳ generated CODESERVER_PASSWORD: $pw"
    yellow "    (saved in clawdstacc.conf — change it any time)"
  else
    sed -i '' 's|^ENABLE_CODESERVER=.*|ENABLE_CODESERVER="false"|' clawdstacc.conf
    green "  ✓ clawdstacc.conf  (ENABLE_CODESERVER=false → headless)"
  fi
else
  green "  ✓ clawdstacc.conf already exists, leaving it alone"
fi

# --- build the unified binary ---

echo
blue "==> Building bin/clawdstacc"
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/clawdstacc ./cmd/clawdstacc
green "  ✓ bin/clawdstacc ($(du -h bin/clawdstacc | awk '{print $1}'))"

# --- run setup subcommand ---

echo
bold "==> Running ./bin/clawdstacc setup"
echo
./bin/clawdstacc setup

# --- final notes ---

echo
green "═══════════════════════════════════════════════════════════════"
green "  clawdstacc installed."
green "═══════════════════════════════════════════════════════════════"
echo
echo "  Dashboard:   http://localhost:8390"
[ -z "$HEADLESS" ] && echo "  code-server: http://localhost:8443"
echo "  Logs:        tail -f ~/Library/Logs/clawdstacc/*.log"
echo "  Config:      $INSTALL_DIR/clawdstacc.conf"
echo
echo "Day-to-day:"
echo "  $INSTALL_DIR/bin/clawdstacc status      # CLI status overview"
echo "  $INSTALL_DIR/bin/clawdstacc setup       # re-render after editing the config"
echo "  $INSTALL_DIR/bin/clawdstacc teardown    # stop and remove every agent"
echo
echo "Add a project: create a directory like ~/_my-project, then refresh the"
echo "dashboard and click 'Set up' on the new card."
echo
echo "Going public? See README → 'Going public (Cloudflare Tunnel + Access)'."
