# Setup-Anleitung

Schritt-für-Schritt-Anleitung zum Aufsetzen von claude-stack auf einem Mac mini (oder anderem macOS-Host).

## 1. Mac mini vorbereiten

### Standby verhindern

Damit der Mac mini deine Sessions dauerhaft am Laufen hält:

```bash
sudo pmset -a sleep 0
sudo pmset -a disablesleep 1
sudo pmset -a powernap 1
sudo pmset -a womp 1
```

In den Systemeinstellungen unter "Batterie / Energie" zusätzlich "Mac automatisch nach Inaktivität schlafen lassen verhindern" aktivieren. Display darf gerne in den Energiesparmodus — nur das System soll wach bleiben.

### Feste IP im Heimnetz

Für stabilen Zugriff sollte der Mac mini eine reservierte IP per DHCP-Reservation im Router bekommen. Die meisten Router (Fritzbox, Unifi, etc.) bieten das unter "DHCP-Server / Statische Leases / IP-Reservierung". MAC-Adresse des Mac mini findest du in den Systemeinstellungen unter "Netzwerk → Details → Hardware".

Notiere dir die IP — du erreichst code-server und Dashboard später unter dieser Adresse.

### Remote-Zugang von außerhalb

Wenn du auch von unterwegs (Mobilfunk, fremdes WLAN) auf deinen Mac mini willst, brauchst du einen VPN-Zugang ins Heimnetz. claude-stack ist agnostisch — es geht jeder VPN, solange er den Mac mini per IP erreichbar macht. Üblich:

- **Wireguard auf dem Router** (Fritzbox 7590+, OPNsense, Unifi UDM, etc.) — empfohlen, wenn dein Router das unterstützt
- **OpenVPN auf dem Router** — Standardlösung, etwas älter aber stabil
- **Wireguard/OpenVPN auf einem Linux-Server im Heimnetz** — wenn der Router selbst kein VPN kann
- **Apple's eingebautes IPSec** — geht, ist aber zickig zu konfigurieren

Wichtig: Der VPN-Tunnel muss dir den Mac mini per IP erreichbar machen. Wenn du mit dem VPN verbunden bist, sollte `ping <mac-mini-ip>` funktionieren — dann passt's.

claude-stack selbst exponiert keine Ports nach außen. Alle Dienste binden auf `0.0.0.0`, das heißt sie sind im Heimnetz erreichbar, aber nicht aus dem Internet. Über VPN bist du logisch im Heimnetz und erreichst sie ganz normal.

## 2. Tools installieren

```bash
# Homebrew (falls noch nicht vorhanden)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Stack-Dependencies
brew install tmux code-server python@3.12

# Claude Code CLI
# (Native Installer — siehe https://claude.com/code für aktuelle Methoden)
curl -fsSL https://claude.com/install.sh | bash
```

Nach der Claude-Code-Installation einmalig anmelden:

```bash
claude
# /login befolgen — Pro oder Max Plan nötig für Remote Control
```

## 3. Shell-Setup für tmux und code-server (wichtig!)

Hintergrund: launchd startet Dienste mit einer minimalen Umgebung — keine `~/.zshrc` wird gelesen, kein Oh My Zsh, kein PATH-Setup. Damit deine Terminal-Erfahrung in tmux und im integrierten code-server-Terminal trotzdem mit deiner gewohnten zsh-Konfiguration läuft, sind zwei Dinge zu tun:

**a) Die `vscode-settings.json.tmpl` (vom Setup automatisch verteilt) nutzt `zsh -l -i` als Default-Profile** — heißt das integrierte Terminal in code-server wird als Login-Shell gestartet, lädt deine `.zprofile` und `.zshrc`, hat damit Theme, Plugins, Aliases. Das passiert automatisch.

**b) Die `claude.plist.tmpl` startet nach Claude-Exit eine `zsh -l -i`** — damit du auch in der tmux-Pane (wenn Claude mal nicht läuft) deine volle Shell-Umgebung hast.

**c) Optional aber empfohlen: `~/.tmux.conf` anlegen** damit auch *neue* tmux-Windows (Ctrl+b c innerhalb einer Session) Login-Shells starten:

```bash
cp ~/claude-stack/tmux.conf.example ~/.tmux.conf
# ggf. anpassen, dann:
tmux kill-server  # frischen Start für alle Sessions
```

(Das `kill-server` ist okay, weil deine launchd-Plists die Sessions sofort wieder neu starten — und Claude continueert über `--continue` deine Conversation.)

**Verifizieren:** Nach Setup einmal in eine tmux-Session attachen und prüfen:

```bash
tmux attach -t projekt1
# Innerhalb: einmal Ctrl+b c für ein neues Window
echo $SHELL              # /bin/zsh
echo $ZSH                # sollte deinen oh-my-zsh-Pfad zeigen
which brew               # sollte gefunden werden
```

Wenn alle drei Antworten plausibel sind, ist deine Shell-Env in tmux korrekt verfügbar.

## 4. Projektordner anlegen

claude-stack erwartet alle deine Projekte in einem Ordner mit Underscore-Prefix im Home-Verzeichnis (oder einem anderen Glob-Pattern, das du in `stack.conf` setzt). Beispiel:

```
~/_project1/    # Hauptprojekt
~/_project2/
~/_clientwork/
```

Die Underscores helfen, die Stack-verwalteten Projekte von deinen anderen Ordnern abzugrenzen. Im Dashboard und in der Claude-App tauchen sie ohne Underscore auf (`project1`, `project2`, `clientwork`).

Innerhalb jedes Projektordners können beliebig viele Repos liegen — claude-stack startet `claude` immer auf dem Projektordner als Working Directory.

## 5. Repo klonen und konfigurieren

```bash
git clone <dein-repo-url> ~/claude-stack
cd ~/claude-stack

# Config aus Vorlage erzeugen
cp stack.conf.example stack.conf

# Anpassen: mindestens CODESERVER_PASSWORD ändern
$EDITOR stack.conf
```

Die wichtigsten Werte in `stack.conf`:

| Variable | Default | Wozu |
|---|---|---|
| `PROJECTS_GLOB` | `$HOME/_*` | Wo deine Projektordner liegen |
| `CODESERVER_BIND` | `0.0.0.0:8443` | Wo code-server lauscht |
| `CODESERVER_PASSWORD` | `CHANGE_ME` | **Unbedingt ändern.** `openssl rand -base64 24` |
| `DASHBOARD_PORT` | `8390` | Port für das Status-Dashboard |
| `CLAUDE_CONTINUE` | `true` | `--continue` beim Auto-Start |
| `BREW_PREFIX` | `/opt/homebrew` | Apple Silicon. Intel: `/usr/local` |

## 6. Setup ausführen

```bash
./bin/setup-stack.sh
```

Das Script:
- prüft ob alle Tools installiert sind
- generiert pro Projekt eine launchd-Plist (`~/Library/LaunchAgents/com.user.claude.<name>.plist`)
- generiert pro Projekt `.vscode/tasks.json` und `.vscode/settings.json` für Auto-Attach
- generiert die code-server- und Dashboard-Plists
- lädt alles in launchd

Idempotent — beliebig oft ausführbar. Wenn du Projekte hinzufügst oder die Config änderst, einfach nochmal laufen lassen.

## 7. Status prüfen

```bash
./bin/stack-status.sh
```

Du solltest sehen: code-server läuft, Dashboard läuft, jedes Projekt hat tmux + agent + (nach erstem Start) eine Session.

Im Browser:
- code-server: `http://<mac-mini-ip>:8443`
- Dashboard: `http://<mac-mini-ip>:8390`

Auf dem Phone: Claude-App → Code-Tab. Die Sessions sollten als `<hostname>-<projektname>-<random>` auftauchen, jeweils mit grünem Status-Dot.

## 8. Erstes Mal mit code-server arbeiten

Im Browser code-server öffnen, Passwort eingeben. Beim ersten Öffnen eines Projekts (`File → Open Folder → ~/_project1`):

1. VSCode fragt: "Do you trust the authors of the files?" → Yes
2. VSCode fragt: "Allow automatic tasks?" → Allow Once oder Always Allow
3. Das Terminal-Panel öffnet sich automatisch und attached an die laufende tmux-Session

Beim nächsten Öffnen des Projekts geht alles ohne Rückfrage.

## 9. Auto-Login auf dem Mac mini

Damit nach einem Stromausfall alles automatisch hochfährt, muss der Mac mini sich automatisch anmelden:

Systemeinstellungen → Benutzer & Gruppen → Automatische Anmeldung → dein User.

Außerdem unter "Allgemein → Anmeldeobjekte" prüfen, dass nichts bei der Anmeldung blockiert. **FileVault muss deaktiviert sein**, sonst kann der Mac mini nach Stromausfall nicht von alleine starten — beim Boot würde er auf Passwort-Eingabe warten.

## 10. Neue Projekte hinzufügen

Einfach einen neuen Ordner mit Underscore-Prefix anlegen und `setup-stack.sh` nochmal laufen lassen:

```bash
mkdir ~/_newproject
cd ~/claude-stack && ./bin/setup-stack.sh
```

Setup ist idempotent, bestehende Sessions werden kurz neu geladen, das neue Projekt taucht überall auf.

## Fertig

Das war's. Ab jetzt: Mac mini läuft 24/7, alle Sessions persistent, vom Phone und Browser steuerbar. Bei Problemen: `docs/TROUBLESHOOTING.md`.
