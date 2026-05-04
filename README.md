# claude-stack

Persistente, fernsteuerbare Claude-Code-Sessions auf einem macOS-Host (Mac mini, Workstation), erreichbar von Phone, Browser und Laptop.

Ein einziger code-server hostet alle Projekte über den Browser. Pro Projekt läuft ein eigener `claude --rc`-Prozess in einer tmux-Session, gestartet und überwacht von launchd. Alle Sessions sind über die Claude iOS/Android-App als Remote Control erreichbar. Ein Dashboard zeigt den Status aller Sessions auf einen Blick und erlaubt Restart per Klick.

> **Hinweis für Weiterentwicklung mit Claude Code:** Diese README ist bewusst ausführlich gehalten, inklusive Architektur-Erklärung und Designentscheidungen, damit Claude Code beim Erweitern des Repos den nötigen Kontext direkt aus der README ziehen kann. Beim Editieren bitte diese Struktur erhalten — Setup-/User-Doku oben, Architektur in der Mitte, Development-Notes unten.

---

## Was du am Ende hast

- Mac mini bootet → alle Sessions starten automatisch
- Vom Phone aus: Claude-App → "Code"-Tab → Liste deiner Projekte, jedes als eigene Remote-Control-Session
- Vom Browser aus (im Heimnetz oder via VPN): `http://<mac-mini-ip>:8443` → VSCode mit allen Projekten, Terminal-Panel hängt sich automatisch an die laufende Claude-Session an
- Dashboard: `http://<mac-mini-ip>:8390` → Statusübersicht, Restart-Buttons
- `/clear`, `/compact`, alle Slash-Commands funktionieren von überall
- Sessions überleben Reboots (`claude --continue`)
- Volle zsh-Login-Shell-Umgebung (Oh My Zsh, Theme, Plugins) sowohl im VSCode-Terminal als auch in der tmux-Pane

## Quickstart

```bash
git clone <dein-repo-url> ~/claude-stack
cd ~/claude-stack

# Konfiguration anlegen (einmalig)
cp stack.conf.example stack.conf
$EDITOR stack.conf       # mindestens CODESERVER_PASSWORD ändern

# Optional aber empfohlen: tmux Konfiguration für Login-Shells
cp tmux.conf.example ~/.tmux.conf

# Setup ausführen — generiert Plists, VSCode-Configs, lädt launchd
./bin/setup-stack.sh

# Status prüfen
./bin/stack-status.sh
```

Nach dem Setup öffnest du im Browser `http://<mac-mini-ip>:8443` (code-server) und `http://<mac-mini-ip>:8390` (Dashboard).

## Voraussetzungen

- macOS (getestet auf Apple Silicon, sollte auf Intel auch laufen — `BREW_PREFIX` in der Config anpassen)
- [Homebrew](https://brew.sh)
- `tmux`, `code-server`, `claude` (Claude Code CLI), `python3` — werden von `setup-stack.sh` geprüft, fehlende werden gemeldet
- Claude.ai Pro oder Max Plan (Remote Control benötigt das)
- VPN-Zugang ins Heimnetz für Remote-Erreichbarkeit (Wireguard auf dem Router, OpenVPN, etc.) — claude-stack ist VPN-agnostisch, alles bindet auf `0.0.0.0` und ist via lokale IP erreichbar

Installation:

```bash
brew install tmux code-server python@3.12
curl -fsSL https://claude.com/install.sh | bash
claude       # einmal /login durchgehen
```

## Repository-Struktur

```
claude-stack/
├── README.md                  # diese Datei (Setup + Architektur + Dev)
├── stack.conf.example         # Konfigurationsvorlage (nach stack.conf kopieren)
├── tmux.conf.example          # empfohlene ~/.tmux.conf (optional)
├── bin/
│   ├── setup-stack.sh         # Hauptscript: erzeugt Plists und VSCode-Configs aus Templates
│   ├── teardown-stack.sh      # alles wieder entfernen
│   ├── stack-status.sh        # CLI-Statusübersicht
│   └── dashboard.py           # Web-Dashboard mit Restart-Buttons (Python stdlib only)
├── templates/
│   ├── claude.plist.tmpl      # tmux+claude Session pro Projekt
│   ├── codeserver.plist.tmpl  # code-server (single instance)
│   ├── dashboard.plist.tmpl   # Dashboard-Service
│   ├── vscode-tasks.json.tmpl # auto-attach an tmux beim Folder-Open
│   └── vscode-settings.json.tmpl  # zsh-login als Default-Profile + tmux-claude
└── docs/
    ├── SETUP.md               # ausführliche Setup-Anleitung
    └── TROUBLESHOOTING.md     # häufige Probleme + Lösungen
```

## Verwaltung im Alltag

```bash
# Status aller Komponenten
./bin/stack-status.sh

# Eine Session manuell neu starten
launchctl kickstart -k "gui/$(id -u)/com.user.claude.<projectname>"

# Logs einsehen
tail -f ~/Library/Logs/claude-stack/*.log

# Stack neu generieren (z.B. nach Änderungen an stack.conf)
./bin/setup-stack.sh

# Stack komplett entfernen
./bin/teardown-stack.sh
```

---

## Architektur

### Design-Ziele

1. **Persistenz:** Sessions überleben Browser-Tabs, Netzwerkabbrüche, Reboots
2. **Multi-Access:** Phone-App, Browser-VSCode, SSH+tmux greifen auf dieselbe Session zu
3. **Native, kein Container/VM:** macOS-launchd verwaltet alles direkt — Claude und Tools haben volle Filesystem- und MCP-Berechtigung
4. **Idempotent:** `setup-stack.sh` kann beliebig oft laufen, regeneriert alles aus Templates
5. **Selbst-heilend:** launchd respawnt gecrashte Prozesse, tmux-Sessions bleiben unabhängig vom Frontend

### Das Schichtenmodell

```
┌──────────────────────────────────────────────────────────┐
│  Phone (Claude App)    Browser (code-server)    Laptop   │
│        │                      │                   │      │
│        │ Remote Control        │ HTTPS              │ SSH  │
│        │ (Anthropic API)       │                   │      │
└────────┼──────────────────────┼───────────────────┼──────┘
         │                      │                   │
         ▼                      ▼                   ▼
┌──────────────────────────────────────────────────────────┐
│                    Mac mini (macOS)                      │
│  ┌───────────────────────────────────────────────────┐   │
│  │  launchd (Init-System, startet alle Services)     │   │
│  └─────┬────────────────┬─────────────┬──────────────┘   │
│        │                │             │                  │
│  ┌─────▼─────┐   ┌──────▼──────┐  ┌──▼─────────┐        │
│  │ tmux      │   │ code-server │  │ dashboard  │        │
│  │ session   │   │ (port 8443) │  │ (port 8390)│        │
│  │ ─────────│   └─────────────┘  └────────────┘        │
│  │ claude    │                                          │
│  │ --rc      │                                          │
│  └───────────┘                                          │
│                                                         │
│  ~/.claude/projects/-USERS-NAME-_projektname/           │
│    └─ session-<uuid>.jsonl   (Conversation-History)     │
└──────────────────────────────────────────────────────────┘
```

### Komponenten und ihre Verantwortlichkeiten

**launchd** ist das Init-System von macOS. Wir registrieren drei Arten von Plists in `~/Library/LaunchAgents/`:

- `com.user.claude.<name>.plist` — eine pro Projekt, startet tmux+claude
- `com.user.codeserver.plist` — single instance, code-server auf `~`
- `com.user.claude-dashboard.plist` — single instance, Status-Dashboard

Plists haben `RunAtLoad: true` (Start beim Login) und `KeepAlive: { SuccessfulExit: false }` (nur respawnen bei Fehler). Die Claude-Plists sind so gebaut, dass sie *idempotent* sind — wenn die tmux-Session schon existiert, beendet sich der Plist-Prozess sauber.

**tmux** ist die Persistenzschicht für `claude`. Eine Session pro Projekt, benannt nach dem Projektordner (ohne Underscore-Prefix). Sessions bleiben am Leben unabhängig von ihrem Frontend — Browser-Tab zu, SSH-Disconnect, alles egal.

**claude --rc** ist Claude Code mit aktivem Remote Control. Jede Instanz registriert sich mit der Anthropic-API und ist als eigene Session in der Claude-App erreichbar. Über `--remote-control-session-name-prefix <name>` bekommen die Sessions sinnvolle Namen.

**code-server** ist VSCode im Browser, gemounted auf `~`. Pro Projekt enthält der Ordner eine `.vscode/`-Config mit:
- `tasks.json` — `runOn: folderOpen` triggert ein Terminal-Panel das `tmux attach -t <name>` ausführt
- `settings.json` — `zsh-login` als Default-Terminal-Profile (volle Login-Shell mit Oh My Zsh)

**dashboard.py** ist ein Python-Webserver (stdlib only, keine Dependencies) auf Port 8390, der den Status aller Komponenten zeigt und per POST `/api/restart/<name>` einen `launchctl kickstart -k` ausführt.

### Datenfluss am Beispiel "User klickt von Phone aus eine Frage in Project1"

1. User tippt in Claude-App auf Project1 → Tap → Frage eintippen → Senden
2. Claude-App sendet HTTPS-Request an Anthropic API
3. Anthropic API routet zur registrierten `claude --rc`-Instanz auf dem Mac mini (über die outbound HTTPS-Connection die der Prozess offenhält — keine inbound Ports)
4. Claude-Prozess auf dem Mac mini empfängt die Frage, führt Aktionen lokal aus (File-Reads, Tool-Calls, MCP-Aufrufe)
5. Antwort fließt zurück über den gleichen Weg zur App

Wenn der User parallel ein Browser-Tab auf code-server hat und dort dasselbe Projekt offen ist, sieht er die Antwort gleichzeitig im integrierten Terminal — weil das tmux-attach an denselben `claude`-Prozess gekoppelt ist.

### Shell-Environment-Behandlung

Das ist eine Stelle wo macOS launchd-Verwaltung tricky ist: launchd-gestartete Prozesse erben **nicht** die User-Shell-Umgebung (kein `.zshrc`, kein Oh My Zsh, kein PATH-Setup). Damit das Terminal-Erlebnis trotzdem normal ist, lösen wir das an drei Stellen:

1. **claude.plist.tmpl** ruft nach Claude-Exit explizit `exec /bin/zsh -l -i` auf — falls Claude mal stirbt, fällt die tmux-Pane in eine Login-Shell
2. **vscode-settings.json.tmpl** setzt `zsh-login` (mit `-l -i`) als Default-Profile in code-server — integrierte Terminals laden die User-Shell-Config
3. **tmux.conf.example** (optional) setzt `default-command "/bin/zsh -l"` — neue Windows in laufenden Sessions starten als Login-Shell

Das ist der häufigste Grund warum "es sieht kaputt aus" beim ersten Setup. Siehe `docs/TROUBLESHOOTING.md` Abschnitt "Terminal hat kein Theme / keine Plugins".

### Persistenz-Marker für `--continue`

`claude --continue` benötigt eine vorher existierende Session. Beim allerersten Start gibt es die nicht — also würde `--continue` fehlschlagen. Wir lösen das mit einer Marker-Datei `<projekt>/.claude/.has-session`:

- Erster Start: Marker existiert nicht → `claude` ohne `--continue` → nach erfolgreichem Start wird Marker gesetzt
- Folgende Starts: Marker existiert → `claude --continue` → setzt die letzte Session fort

Die Logik liegt in `templates/claude.plist.tmpl`. Wenn `~/.claude/projects/` extern aufgeräumt wird, kann der Marker out-of-sync gehen — siehe Troubleshooting.

---

## Development & Erweiterung

### Konventionen

- **Bash-Scripts** in `bin/` sind `set -euo pipefail`, kompatibel mit macOS-Default-Bash 3.2 (heißt: keine `mapfile`/`readarray`, keine `${var,,}`-Casing-Tricks).
- **Templates** verwenden `__UPPERCASE__`-Platzhalter, ersetzt durch `render_template()` in `setup-stack.sh`. Neue Templates folgen demselben Schema.
- **Plist-Labels** folgen `com.user.<role>.<name>`. Konsistent halten — `stack-status.sh` und `dashboard.py` parsen das Format.
- **Logs** gehen nach `~/Library/Logs/claude-stack/<service>.{log,err}`. Bitte nicht nach `/tmp` (volatile).
- **Python-Code** in `dashboard.py` ist bewusst stdlib-only — keine Dependencies, kein venv. Wenn das mal nicht reicht: dann mit klarem Kommentar dokumentieren warum.

### Häufige Erweiterungen

**Neue Konfigurationsoption hinzufügen:**
1. Default in `stack.conf.example` dokumentieren
2. In `setup-stack.sh` mit `: "${VAR:=default}"` Default setzen
3. Falls in Templates verwendet: `render_template`-Aufrufe mit `KEY=val` erweitern
4. Falls für Dashboard relevant: `load_conf()` in `dashboard.py` um Schlüssel ergänzen

**Neuen Service zur Stack hinzufügen** (z.B. einen MCP-Bridge-Server, einen weiteren Webserver):
1. Template `templates/<service>.plist.tmpl` anlegen
2. In `setup-stack.sh` einen `render_template`-Block für den Service ergänzen
3. Plist zu `ALL_PLISTS`-Array hinzufügen für reload
4. Status-Check in `stack-status.sh` und `dashboard.py` ergänzen
5. Teardown wird automatisch funktionieren wenn das Label `com.user.*` matched

**Pro-Projekt-Customization** (z.B. unterschiedliche Claude-Flags pro Projekt):
- Aktuell global via `CLAUDE_EXTRA_FLAGS`. Erweiterung: pro-Projekt `<projekt>/.claude-stack.yml` definieren, in `setup-stack.sh` lesen und beim Template-Rendern überschreiben.

**Dashboard erweitern:**
- Frontend-Template ist als Python-String in `dashboard.py` (HTML-Konstante). Klein halten — wenn das größer wird, in eine separate Datei auslagern und beim Server-Start einlesen.
- API-Endpunkte: alle unter `/api/`. POST-Endpunkte sollen Inputs strikt validieren (siehe `restart_project()` als Vorbild).
- Stop/Start einzelner Projekte könnte ergänzt werden (`/api/stop/<name>`, `/api/start/<name>`).

### Was bewusst NICHT drin ist (und warum)

- **Kein TLS:** code-server und Dashboard sprechen HTTP. Empfehlung ist VPN ins Heimnetz statt direkter Internet-Exposure. Wenn TLS nötig: Reverse-Proxy davorsetzen (Caddy oder nginx auf dem Mac mini) oder Tailscale Serve nutzen.
- **Keine Auth-Layer auf dem Dashboard:** Schutz ist die Netzwerk-Schicht (VPN). Wenn das Dashboard von außerhalb des Heimnetzes erreichbar werden soll, eine Auth-Schicht ergänzen.
- **Keine Container/VM:** Bewusste Entscheidung — siehe Architektur-Sektion.
- **Keine VSCode Settings Sync:** code-server's Open VSX und Microsofts Marketplace sind nicht sync-kompatibel. User installiert Extensions in code-server manuell nach Bedarf.
- **Kein automatisches Update:** Tools werden via `brew upgrade` aktualisiert, nicht via dieses Repo. Empfehlung: `brew pin code-server tmux` für Stabilität, gezielt unpinnen + upgraden.

### Tests / Verifikation

Aktuell keine automatisierten Tests im Repo (Setup ist macOS-spezifisch und hardware-abhängig). Manuelle Verifikation:

```bash
# Syntax-Checks
bash -n bin/*.sh
python3 -m py_compile bin/dashboard.py

# Smoke-Test des Setups (auf macOS mit erfüllten Voraussetzungen)
./bin/setup-stack.sh
./bin/stack-status.sh    # alles ✓?
curl -sf http://localhost:8390/api/status | python3 -m json.tool
./bin/teardown-stack.sh  # sauberes Entfernen?
```

Ein zukünftiger Pfad wäre eine ShellCheck-Pipeline für die Bash-Scripts und vielleicht ein dry-run-Modus für `setup-stack.sh`, der die generierten Plists nur ausgibt statt sie zu installieren.

## Lizenz

Mach damit was du willst. MIT-style.
