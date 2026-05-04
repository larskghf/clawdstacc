# Troubleshooting

## "claude: command not found" beim Setup

`setup-stack.sh` prüft ob `claude` im PATH ist. Falls die Native-Installation den Pfad nicht in deine Shell-Config geschrieben hat, manuell hinzufügen:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

Oder den vollständigen Pfad rausfinden mit `which claude` und in `stack.conf` als zusätzlichen PATH-Eintrag konfigurieren.

## Sessions tauchen nicht in der Claude-App auf

Erste Checks:

```bash
./bin/stack-status.sh
```

- `tmux ●` rot? → tmux-Session existiert nicht. Logs anschauen: `tail ~/Library/Logs/claude-stack/claude-<name>.err`
- `agent ●` rot? → launchd hat den Plist nicht geladen. `launchctl list | grep com.user.claude` zeigt was wirklich geladen ist.
- tmux ✓ aber claude ✗? → tmux läuft, aber Claude ist gecrasht oder noch nicht gestartet. `tmux attach -t <name>` und schauen was dort passiert.

In der Claude-App: ist der richtige Account eingeloggt? Remote Control braucht eine Pro- oder Max-Subscription. Wenn du auf einem Free-Account bist, taucht keine Session auf.

## Terminal in code-server hat kein Theme / keine Plugins / Befehle fehlen

Das ist das klassische "launchd-Env vs. interaktive Shell"-Problem. launchd startet code-server mit minimaler Umgebung, das integrierte Terminal erbt die.

**Default-Setup sollte das schon lösen:** Das von uns generierte `vscode-settings.json` setzt `zsh-login` (mit `-l -i`) als Default-Profil. Wenn das nicht greift, prüfen:

1. **Settings nicht überschrieben?** In code-server `Cmd+,` öffnen, Default Profile (osx) suchen, sollte `zsh-login` sein. Falls nicht: User-Settings haben Workspace-Settings überschrieben — in den User-Settings denselben Wert setzen.

2. **`.zshrc` lädt nicht durch?** In einem Terminal in code-server testen:
   ```bash
   echo $0           # sollte "-zsh" sein (mit Bindestrich = Login-Shell)
   echo $ZSH         # sollte oh-my-zsh-Pfad sein
   echo $ZSH_THEME   # dein Theme
   ```
   Wenn `$0` ohne Bindestrich → keine Login-Shell. Profile-Args prüfen, `-l` muss drin sein.

3. **`.zshrc` hat einen Fehler?** Wenn der Output verdächtig leer ist, manuell testen:
   ```bash
   /bin/zsh -l -i -c 'echo OK'
   ```
   Bei Fehlern siehst du sie hier.

4. **Brew-Pfade fehlen?** Apple Silicon hat einen anderen Default-PATH. Dieser Block sollte in `~/.zprofile` oder `~/.zshrc` stehen:
   ```bash
   eval "$(/opt/homebrew/bin/brew shellenv)"
   ```

## Terminal in tmux-Session hat kein Theme / keine Plugins

Selbe Ursache wie oben. Die Plist startet nach Claude-Exit `zsh -l -i`, das sollte funktionieren. Falls nicht:

```bash
tmux attach -t <projekt>
echo $0                    # in der Pane prüfen
```

Falls die Pane gerade in Claude steckt, mit Ctrl+b c ein neues Window aufmachen — das nutzt aber die `default-command` aus `~/.tmux.conf`. Wenn du `tmux.conf.example` noch nicht übernommen hast, kommt da die System-Default-Shell ohne Login-Flag.

Fix:

```bash
cp ~/claude-stack/tmux.conf.example ~/.tmux.conf
tmux kill-server   # Sessions starten via launchd neu, mit der neuen Config
```

## "Unable to connect to claude.ai" oder Network-Timeout

Claude Code Remote Control benötigt durchgehend Internet. Wenn der Mac mini ~10 Min keine Verbindung hat, beendet sich der Prozess. launchd startet ihn automatisch neu, aber der `--continue`-Flag sorgt dafür dass die Conversation weiterläuft. Wenn das oft passiert: check WLAN-Stabilität oder verkabel den Mac mini.

## VSCode öffnet kein Terminal beim Folder-Open

Drei mögliche Ursachen:

1. **Auto-Tasks nicht erlaubt:** in code-server unter Settings (Cmd+,) suchen nach `task.allowAutomaticTasks` und auf `on` setzen. Sollte unsere `settings.json` schon erledigen, aber kann von User-Settings überschrieben werden.

2. **`runOn: folderOpen` feuert nicht:** das passiert bei VSCode manchmal nach Updates. Workaround: einmal `Tasks: Run Task → Attach Claude (tmux)` aus der Command-Palette ausführen.

3. **tmux-Session existiert nicht:** der Task macht `tmux attach || tmux new`, also sollte das selbst-heilend sein. Falls nicht: `./bin/stack-status.sh` zeigt was los ist.

## "claude --continue" findet keine Session

Beim ersten Start gibt's keine Session zum Continuen. Der Setup nutzt eine Marker-Datei (`<projekt>/.claude/.has-session`), die beim ersten erfolgreichen Start gesetzt wird.

Falls die mal außer Sync ist (z.B. weil du `~/.claude/projects/` manuell aufgeräumt hast):

```bash
rm ~/_projektname/.claude/.has-session
launchctl kickstart -k "gui/$(id -u)/com.user.claude.projektname"
```

## launchd respawnt den Prozess in Endlosschleife

Logs:

```bash
log show --predicate 'subsystem == "com.apple.xpc.launchd"' --last 10m | grep com.user
```

Häufigste Ursache: `KeepAlive` ist zu aggressiv. In den Plists nutzen wir `SuccessfulExit: false` für Claude — das heißt: nur respawnen wenn der Prozess mit Fehlercode endet. Bei sauberem Exit (z.B. du killst die Session manuell) lässt launchd ihn in Ruhe.

Wenn du eine Session bewusst stoppen willst:

```bash
launchctl bootout "gui/$(id -u)/com.user.claude.projektname"
tmux kill-session -t projektname
```

Wieder anschalten:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.user.claude.projektname.plist
```

## Dashboard zeigt "agent: no agent" obwohl `launchctl list` ihn zeigt

Das Dashboard nutzt einen Regex-Match auf `launchctl list`. Bei sehr alten macOS-Versionen kann das Format leicht anders sein. Workaround: in `bin/dashboard.py` die Funktion `agent_loaded` anpassen oder die Detection ignorieren — wenn `tmux ●` und `claude ●` grün sind, ist alles gut, der Agent-Check ist sekundär.

## Code-Server "Connection lost" nach Idle

Standardmäßig hält code-server WebSocket-Verbindungen offen. Wenn du über einen Reverse-Proxy gehst, kann der nach längerer Idle-Zeit die Verbindung droppen. Browser-Tab schließen und neu öffnen — die Backend-Session läuft weiter, du landest wieder im selben State.

## Code-Server Browser-Tab zu, Session weg?

Nein. Wenn du im integrierten Terminal `tmux attach` gemacht hast und das Browser-Tab schließt, läuft die tmux-Session unbehindert weiter. Beim nächsten Browser-Öffnen feuert `runOn: folderOpen` und attached wieder. Falls aus irgendwelchen Gründen das Auto-Attach fehlschlägt: einfach im Terminal `tmux attach -t <projektname>` manuell ausführen.

## Permissions / "Operation not permitted"

macOS verlangt für viele Operationen explizite Berechtigungen. Wenn du in den Logs Permission-Fehler siehst (z.B. claude kann keine Files lesen):

Systemeinstellungen → Privatsphäre & Sicherheit → "Vollständiger Festplattenzugriff" → den Terminal-Emulator hinzufügen, in dem du das Setup ausgeführt hast (Terminal.app, iTerm2, etc.). Reboot.

Bei manchen Settings hilft auch, `bash` selbst dort hinzuzufügen.

## Stack komplett neu aufsetzen

Wenn du das Gefühl hast, die Konfiguration ist durcheinander:

```bash
./bin/teardown-stack.sh    # entfernt alle Plists, beendet Prozesse
./bin/setup-stack.sh       # erzeugt alles frisch
```

Das ist sicher — deine `~/.claude/projects/`-Conversations bleiben erhalten, also keine Datenverluste.

## Alle Logs auf einmal anschauen

```bash
tail -f ~/Library/Logs/claude-stack/*.log ~/Library/Logs/claude-stack/*.err
```

Oder mit `multitail` für nebeneinander:

```bash
brew install multitail
multitail ~/Library/Logs/claude-stack/claude-*.log
```
