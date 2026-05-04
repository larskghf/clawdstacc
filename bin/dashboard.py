#!/usr/bin/env python3
"""
claude-stack dashboard

Minimal web server (Python stdlib only). Shows the status of every project
session and lets you restart any of them with one click. Reads configuration
from stack.conf.

Available at http://<host>:<DASHBOARD_PORT> (default 8390).
"""
import glob
import http.server
import json
import os
import re
import shlex
import socketserver
import subprocess
import time
from pathlib import Path

# --- Load config ---

CONF_PATH = os.environ.get("CLAUDE_STACK_CONF") or str(
    Path(__file__).resolve().parent.parent / "stack.conf"
)
PORT = int(os.environ.get("CLAUDE_STACK_PORT", "8390"))


def load_conf():
    """Read the bash-style stack.conf — only the variables we care about."""
    cfg = {
        "PROJECTS_GLOB": str(Path.home() / "_*"),
        "CODESERVER_BIND": "0.0.0.0:8443",
        "LOG_DIR": str(Path.home() / "Library/Logs/claude-stack"),
        "EXPLICIT_PROJECTS": [],
    }
    if not os.path.exists(CONF_PATH):
        return cfg
    with open(CONF_PATH) as f:
        text = f.read()

    # Scalar variables
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        m = re.match(r'^([A-Z_]+)=(.*)$', line)
        if not m:
            continue
        key, val = m.group(1), m.group(2).strip()
        if val.startswith("("):
            continue
        val = val.strip('"').strip("'")
        val = val.replace("$HOME", str(Path.home()))
        if key in cfg and not isinstance(cfg[key], list):
            cfg[key] = val

    # EXPLICIT_PROJECTS=( ... ) — bash array. Prefer the active (un-commented) block.
    block_re = re.compile(r'^[ \t]*EXPLICIT_PROJECTS=\(\s*\n(.*?)\n[ \t]*\)\s*$',
                          re.MULTILINE | re.DOTALL)
    for m in block_re.finditer(text):
        # Is the block's leading line commented out? Check it.
        start = m.start()
        line_start = text.rfind("\n", 0, start) + 1
        first_line = text[line_start:text.find("\n", start)]
        if first_line.lstrip().startswith("#"):
            continue
        items = []
        for raw in m.group(1).splitlines():
            raw = raw.strip()
            if not raw or raw.startswith("#"):
                continue
            raw = raw.strip('"').strip("'")
            raw = raw.replace("$HOME", str(Path.home()))
            items.append(raw)
        if items:
            cfg["EXPLICIT_PROJECTS"] = items
            break
    return cfg


CFG = load_conf()
HOME = Path.home()


# --- Utilities ---

def sh(cmd, default=""):
    """Run a shell command and return its stdout, or `default` on failure."""
    try:
        return subprocess.check_output(
            cmd, shell=True, text=True, stderr=subprocess.DEVNULL, timeout=5
        ).strip()
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return default


def list_projects():
    """Sorted list of Path objects — EXPLICIT_PROJECTS takes precedence over the glob."""
    if CFG.get("EXPLICIT_PROJECTS"):
        paths = CFG["EXPLICIT_PROJECTS"]
    else:
        paths = glob.glob(CFG["PROJECTS_GLOB"])
    return sorted([Path(p) for p in paths if os.path.isdir(p)])


def project_name(path):
    """Directory name without the leading underscore."""
    return path.name.lstrip("_")


def tmux_session_info(name):
    """Status of a tmux session."""
    out = sh(f"tmux display-message -p -t {shlex.quote(name)} "
             f"'#{{session_activity}}|#{{pane_pid}}'")
    if not out or "|" not in out:
        return {"alive": False}
    activity_ts, pid = out.split("|", 1)
    try:
        ago = int(time.time()) - int(activity_ts)
    except ValueError:
        ago = None
    return {"alive": True, "pid": pid, "idle_seconds": ago}


def claude_running_in_pane(pane_pid):
    """Is a claude-like process currently running inside the given tmux pane?"""
    if not pane_pid:
        return False
    # Check the pane pid itself (the new plist runs claude directly so
    # claude IS the pane pid) and any descendants (older shell-wrapped
    # invocations have claude as a child of zsh).
    pids = [pane_pid] + sh(f"pgrep -P {shlex.quote(pane_pid)}").splitlines()
    for pid in pids:
        cmd = sh(f"ps -p {shlex.quote(pid)} -o command=")
        if "claude" in cmd.lower():
            return True
    return False


def latest_session(project_path):
    """Find the most recent JSONL session file for this project."""
    sanitized = str(project_path).replace("/", "-").lstrip("-")
    sessions_dir = HOME / ".claude" / "projects" / f"-{sanitized}"
    if not sessions_dir.exists():
        return None
    jsonls = sorted(sessions_dir.glob("*.jsonl"),
                    key=lambda p: p.stat().st_mtime, reverse=True)
    if not jsonls:
        return None
    latest = jsonls[0]
    return {
        "modified_ago": int(time.time() - latest.stat().st_mtime),
        "size_bytes": latest.stat().st_size,
    }


def agent_loaded(label):
    """Is a launchd agent currently loaded?"""
    out = sh(f"launchctl list | grep -E '\\s{re.escape(label)}$'")
    return bool(out)


def collect_status():
    rows = []
    for path in list_projects():
        name = project_name(path)
        tmux = tmux_session_info(name)
        claude_alive = claude_running_in_pane(tmux.get("pid", ""))
        rows.append({
            "name": name,
            "path": str(path),
            "tmux_alive": tmux["alive"],
            "tmux_idle": tmux.get("idle_seconds"),
            "claude_alive": claude_alive,
            "agent_loaded": agent_loaded(f"com.user.claude-stack.{name}"),
            "session": latest_session(path),
        })
    return {
        "projects": rows,
        "code_server_loaded": agent_loaded("com.user.claude-stack.codeserver"),
        "code_server_bind": CFG["CODESERVER_BIND"],
        "ts": int(time.time()),
    }


def restart_project(name):
    """Restart the tmux+claude session for a project."""
    # Validate the name — only [a-zA-Z0-9_-] is allowed.
    if not re.match(r'^[a-zA-Z0-9_-]+$', name):
        return False, "Invalid project name"
    label = f"com.user.claude-stack.{name}"
    # Kill the tmux session so the next start is fresh.
    sh(f"tmux kill-session -t {shlex.quote(name)} 2>/dev/null", default="")
    # Kickstart the agent.
    uid = os.getuid()
    rc = subprocess.run(
        ["launchctl", "kickstart", "-k", f"gui/{uid}/{label}"],
        capture_output=True, text=True, timeout=10
    )
    if rc.returncode == 0:
        return True, "ok"
    return False, rc.stderr.strip() or rc.stdout.strip() or "unknown error"


# --- HTTP Handler ---

HTML = r"""<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>claude-stack</title>
<style>
  :root {
    --bg: #0f1115; --card: #1a1d24; --muted: #8a93a3; --text: #e6e8eb;
    --ok: #4ade80; --warn: #facc15; --err: #ef4444; --link: #93c5fd;
  }
  * { box-sizing: border-box; }
  body { font-family: -apple-system, system-ui, "SF Pro Text", sans-serif;
         background: var(--bg); color: var(--text); margin: 0;
         padding: max(env(safe-area-inset-top), 1rem) 1rem 1rem; }
  h1 { font-size: 1.05rem; margin: 0 0 .5rem; font-weight: 600; opacity: .9; }
  .header-meta { font-size: .75rem; color: var(--muted); margin-bottom: 1rem;
                 font-family: ui-monospace, "SF Mono", monospace; }
  .card { background: var(--card); border-radius: 12px; padding: .9rem 1rem;
          margin-bottom: .6rem; border-left: 4px solid #2a2f3a;
          transition: opacity .2s; }
  .card.ok   { border-left-color: var(--ok); }
  .card.warn { border-left-color: var(--warn); }
  .card.dead { border-left-color: var(--err); opacity: .55; }
  .row { display: flex; justify-content: space-between; align-items: center;
         gap: .5rem; flex-wrap: wrap; }
  .name { font-weight: 600; font-size: 1.05rem; }
  .meta { font-size: .78rem; color: var(--muted);
          font-family: ui-monospace, "SF Mono", monospace; }
  .badges { display: flex; gap: .35rem; flex-wrap: wrap; }
  .b { display: inline-block; font-size: .68rem; padding: 2px 8px;
       border-radius: 99px; background: #2a2f3a; color: var(--muted);
       letter-spacing: .03em; font-weight: 500; }
  .b.live { background: #14532d33; color: #86efac; }
  .b.idle { background: #422006aa; color: #fbbf24; }
  .b.dead { background: #450a0a99; color: #fca5a5; }
  .actions { display: flex; gap: .5rem; margin-top: .65rem; }
  a, button {
    font: inherit; color: var(--link); background: transparent; border: 1px solid #2a2f3a;
    padding: .35rem .7rem; border-radius: 6px; text-decoration: none;
    cursor: pointer; font-size: .82rem;
  }
  button:hover, a:hover { background: #2a2f3a; }
  button.warn { color: var(--warn); border-color: #422006; }
  button:active { transform: translateY(1px); }
  button:disabled { opacity: .5; cursor: not-allowed; }
  .footer { color: var(--muted); font-size: .72rem; margin-top: 1.5rem;
            text-align: center; }
  .toast { position: fixed; bottom: 1rem; left: 50%; transform: translateX(-50%);
           background: var(--card); padding: .6rem 1rem; border-radius: 8px;
           border-left: 3px solid var(--ok); font-size: .85rem;
           opacity: 0; transition: opacity .2s; }
  .toast.show { opacity: 1; }
  .toast.err { border-left-color: var(--err); }
</style></head>
<body>
  <h1>claude-stack</h1>
  <div class="header-meta" id="hdr">loading…</div>
  <div id="root"></div>
  <div class="footer">refresh every 5s</div>
  <div class="toast" id="toast"></div>
<script>
function fmtAgo(s) {
  if (s == null) return "?";
  if (s < 60) return s + "s";
  if (s < 3600) return Math.floor(s/60) + "m";
  if (s < 86400) return Math.floor(s/3600) + "h";
  return Math.floor(s/86400) + "d";
}
function toast(msg, isErr) {
  const t = document.getElementById("toast");
  t.textContent = msg;
  t.className = "toast show" + (isErr ? " err" : "");
  setTimeout(() => t.className = "toast", 2500);
}
async function restart(name, btn) {
  if (!confirm(`Restart session "${name}"?`)) return;
  btn.disabled = true; btn.textContent = "restarting…";
  try {
    const r = await fetch("/api/restart/" + encodeURIComponent(name), {method: "POST"});
    const d = await r.json();
    if (d.ok) { toast("restart ok: " + name); refresh(); }
    else      { toast("error: " + d.error, true); }
  } catch (e) { toast("error: " + e, true); }
  finally { btn.disabled = false; btn.textContent = "restart"; }
}
async function refresh() {
  try {
    const r = await fetch("/api/status");
    const d = await r.json();
    const cs = d.code_server_loaded ? "✓" : "✗";
    document.getElementById("hdr").textContent =
      `code-server: ${cs} ${d.code_server_bind}  ·  ${d.projects.length} projects`;
    const root = document.getElementById("root"); root.innerHTML = "";
    for (const p of d.projects) {
      const cls = !p.tmux_alive ? "dead"
                : (p.claude_alive ? "ok" : "warn");
      const badges = [];
      badges.push(p.tmux_alive
        ? `<span class="b live">tmux</span>`
        : `<span class="b dead">no tmux</span>`);
      badges.push(p.claude_alive
        ? `<span class="b live">claude</span>`
        : `<span class="b idle">no claude</span>`);
      badges.push(p.agent_loaded
        ? `<span class="b live">agent</span>`
        : `<span class="b dead">no agent</span>`);
      const idle = p.tmux_idle != null ? "idle " + fmtAgo(p.tmux_idle) : "";
      const sess = p.session
        ? "session " + fmtAgo(p.session.modified_ago) + " ago"
        : "no session yet";
      const csUrl = "http://" + location.hostname + ":"
                   + d.code_server_bind.split(":").pop()
                   + "/?folder=" + encodeURIComponent(p.path);
      const card = document.createElement("div");
      card.className = "card " + cls;
      card.innerHTML = `
        <div class="row">
          <span class="name">${p.name}</span>
          <span class="badges">${badges.join("")}</span>
        </div>
        <div class="row" style="margin-top:.4rem">
          <span class="meta">${idle}</span>
          <span class="meta">${sess}</span>
        </div>
        <div class="actions">
          <a href="${csUrl}" target="_blank">open in code-server →</a>
          <button class="warn" onclick="restart('${p.name}', this)">restart</button>
        </div>`;
      root.appendChild(card);
    }
  } catch (e) {
    document.getElementById("hdr").textContent = "fetch error: " + e;
  }
}
refresh(); setInterval(refresh, 5000);
</script></body></html>"""


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args, **kwargs):
        pass  # quiet

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/api/status":
            self._json(collect_status())
        elif self.path == "/" or self.path == "/index.html":
            body = HTML.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404); self.end_headers()

    def do_POST(self):
        m = re.match(r'^/api/restart/([a-zA-Z0-9_-]+)$', self.path)
        if not m:
            self._json({"ok": False, "error": "bad path"}, 400)
            return
        name = m.group(1)
        ok, info = restart_project(name)
        self._json({"ok": ok, "error": None if ok else info})


class ReusableServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True


if __name__ == "__main__":
    print(f"claude-stack dashboard on :{PORT}  (conf: {CONF_PATH})")
    with ReusableServer(("0.0.0.0", PORT), Handler) as httpd:
        try:
            httpd.serve_forever()
        except KeyboardInterrupt:
            pass
