# dredge

[![CI](https://github.com/W0rkingChr1s/dredge/actions/workflows/ci.yml/badge.svg)](https://github.com/W0rkingChr1s/dredge/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Geführtes, sicheres Aufräumen für Docker – als ein einzelnes Go-Binary. Der
Nachbau (und Ausbau) eines n8n-Flows, der wöchentlich ungenutzte Docker-Ressourcen
meldet und per Telegram-Buttons zur Freigabe stellt – nur eben ohne n8n, self-contained.

- **Ein Binary**, keine Runtime-Abhängigkeiten. Läuft als systemd-Timer **oder** als Docker-Container.
- **Geführte Einrichtung** im Terminal-UI (`setup`), Einstellungen jederzeit änderbar (`settings`).
- **Interaktive Freigabe** über Telegram: Checkboxen zum Mehrfach-Auswählen, mit Timeout-Default – plus Mail/ntfy/Gotify/Discord über shoutrrr.
- **Safety-first**: Mindestalter, Schutz-Labels, geschützte Volume-Namen, Trockenlauf.
- **Voller Umfang**: dangling & ungenutzte Images, gestoppte Container, ungenutzte Netzwerke, Build-Cache und (extra abgesicherte) Volumes.

## Installation

`dredge` ist ein statisches Binary – auf dem Zielhost muss **kein Go** installiert sein.

**Fertiges Release-Binary (empfohlen)**

```bash
curl -fsSL https://raw.githubusercontent.com/W0rkingChr1s/dredge/main/install.sh | sh
```

Das Skript erkennt die Architektur (amd64/arm64/armv7), lädt das passende
Binary aus dem letzten Release, **prüft die SHA256-Summe** und legt es nach
`/usr/local/bin/dredge`. Ohne root geht es auch:
`PREFIX=$HOME/.local sh install.sh`, eine bestimmte Version mit
`--version v0.1.0`.

**Aus dem Quellcode** (braucht Go ≥ 1.24 auf der Baumaschine)

```bash
make build          # -> dist/dredge, ohne sudo
sudo make install   # -> /usr/local/bin/dredge
```

> `make build` niemals mit `sudo` aufrufen: `sudo` verwirft `$PATH`, dann
> findet root das `go` nicht (`/bin/sh: 1: go: not found`). Falls es doch
> nötig ist: `make build GO=/usr/local/go/bin/go`.

**Als Container** – siehe [Deployment A](#deployment-a--docker-container),
da braucht es gar keine Installation auf dem Host.

## Schnellstart

```bash
# 1. Einrichten (interaktiver Assistent)
sudo mkdir -p /etc/dredge && sudo dredge setup

# 2. Anschauen, was bereinigt würde – ändert nichts
sudo dredge scan

# 3. Kompletter Probelauf ohne Löschen
sudo dredge run --dry-run

# 4. Regelmäßig laufen lassen (systemd-Timer)
sudo dredge install
```

Am Ende von `setup` bietet der Assistent das Einplanen direkt mit an.

## Befehle

| Befehl | Zweck |
|---|---|
| `setup` | Geführte Ersteinrichtung (TUI-Wizard) |
| `settings` (alias `tui`) | Einstellungen im TUI ändern |
| `scan` | Nur anzeigen, was bereinigt würde (read-only) |
| `run` | Voller Lauf: Scan → Freigabe → Bereinigen → Bestätigung |
| `run --dry-run` | Alles simulieren, nichts löschen |
| `run --yes` | Nicht-interaktiv alle `ask`-Typen freigeben |
| `daemon` | Interner Scheduler im Vordergrund (für Container) |
| `install` | systemd service + timer aus dem Schedule erzeugen und aktivieren |
| `install --print` | Units nur ausgeben, nichts schreiben |
| `uninstall` | Timer wieder entfernen (Konfiguration bleibt) |
| `version` | Version anzeigen |

`install-timer` funktioniert weiterhin als veralteter Alias für `install`.

## Modi pro Ressourcentyp

Jeder Typ (Images, Container, Netzwerke, Build-Cache, Volumes) hat einen Modus:

- `off` – wird nie angefasst
- `auto` – wird ohne Nachfrage bereinigt
- `ask` – löst eine Telegram-Freigabe mit Buttons aus

Bei einem Lauf werden `auto`-Typen direkt eingeplant; für `ask`-Typen kommt eine
Telegram-Nachricht mit Buttons.

**Mehrfachauswahl:** Jeder `ask`-Typ hat eine eigene Checkbox mit seiner Größe.
Antippen wählt an bzw. ab, die Nachricht aktualisiert sich sofort:

```
🧹 Alles bereinigen
☑ Dangling Images · 400.0 MB
☐ Ungenutzte Images · 2.1 GB
☑ Verwaiste Volumes ⚠ · 100.0 MB
✅ Auswahl bereinigen · 2 · 500.0 MB
✋ Nichts
```

`✅ Auswahl bereinigen` übernimmt genau das Angekreuzte; `🧹 Alles` und
`✋ Nichts` entscheiden sofort. Ohne Auswahl passiert bei `✅` nichts außer
einem Hinweis – ein Fehlgriff soll nicht als „nichts bereinigen" durchgehen.
Nach der Entscheidung verschwinden die Buttons und die Nachricht zeigt, was
freigegeben wurde. Steht nur ein einziger Typ zur Wahl, bleibt es beim
einfachen Alles/Nichts.

Antwortest du nicht innerhalb von `approval_timeout_minutes`, greift
`on_timeout` (`none` | `dangling` | `all`). Das Zeitlimit läuft ab dem
Absenden – Antippen verlängert es nicht.

## Safety-Rails

- **Mindestalter** (`min_age_hours`): jüngere Objekte bleiben unberührt – schützt frisch gepullte Images.
- **Schutz-Labels** (`protect_labels`): z. B. `janitor.keep=true`. Trag es an einem Image/Volume an, das nie weg soll.
- **Geschützte Volume-Namen** (`protect_volume_names`): Globs wie `*_data`, `portainer_*`.
- **Trockenlauf** (`dry_run` oder `--dry-run`): meldet, löscht aber nichts.
- **Volumes** stehen standardmäßig auf `ask` – Volume-Löschung ist unwiderruflicher Datenverlust.

Der Report zeigt geschützte Objekte separat an (🛡), damit du siehst, was bewusst verschont wird.

## Deployment A – Docker-Container

Der Container plant intern via `daemon`, es braucht also keinen Cron auf dem
Host. Die mitgelieferte [`docker-compose.yml`](docker-compose.yml) bringt
gleich einen socket-proxy mit.

```bash
# Erst-Einrichtung einmalig interaktiv:
mkdir -p config
sudo chown 10001:10001 config          # UID/GID des Nutzers im Image
docker compose run --rm dredge setup   # schreibt ./config/config.yaml
# Danach:
docker compose up -d                   # CMD = daemon, läuft nach Schedule
```

Der Container läuft unpriviligiert als UID/GID `10001`. Gehört `./config` dem
root-Nutzer, kann `setup` nichts speichern – daher das `chown`. Wer das
Verzeichnis lieber dem eigenen Account gibt, setzt in der compose-Datei
`user: "1000:1000"`.

Für den Zugriff auf die Engine-API gibt es zwei Wege:

- **socket-proxy** (empfohlen): `docker.host: http://dockerproxy:2375`. `dredge`
  braucht schreibenden Zugriff, und ein direkt gemounteter Socket ist effektiv
  Root auf dem Host – der Proxy begrenzt das auf die nötigen Endpunkte.
- **Socket direkt**: `docker.host: unix:///var/run/docker.sock` und
  `/var/run/docker.sock` in den Container mounten. Einfacher, weniger abgesichert.

Der **socket-proxy** (Tecnativa `docker-socket-proxy`) muss diese Endpunkte erlauben:

```
PING=1 IMAGES=1 VOLUMES=1 NETWORKS=1 CONTAINERS=1 SYSTEM=1 BUILD=1 POST=1
```

`POST=1` gibt auch die nötigen `DELETE`-/`prune`-Aufrufe frei. Ohne diese Rechte
kann `scan` (read-only) laufen, aber `run` schlägt beim Löschen fehl – die Fehler
werden pro Objekt gemeldet, nichts anderes bricht ab.

## Deployment B – Host-Binary + systemd

```bash
sudo mkdir -p /etc/dredge
sudo dredge setup     # schreibt /etc/dredge/config.yaml
sudo dredge install   # Units erzeugen, daemon-reload, Timer aktivieren
```

`dredge install` erledigt in einem Rutsch:

- übersetzt `schedule.cron` nach `OnCalendar` und hängt `schedule.timezone` an
  (braucht systemd ≥ 252; ältere Versionen bekommen eine Warnung und nutzen die
  Zeitzone des Hosts),
- setzt `TimeoutStartSec` über das Telegram-Freigabe-Timeout, damit systemd
  einen wartenden Lauf nicht abschneidet,
- legt das State-Verzeichnis für die Historie an,
- schreibt eine gehärtete Service-Unit (`ProtectSystem=strict`,
  `NoNewPrivileges`, …),
- führt `daemon-reload` und `enable --now` aus und zeigt den nächsten Lauf.

Nützliche Varianten:

| Aufruf | Wirkung |
|---|---|
| `dredge install --print` | Units nur anzeigen (kein root nötig) |
| `sudo dredge install --enable=false` | schreiben, aber nicht aktivieren |
| `sudo dredge install --exec-path /opt/dredge` | anderen Binärpfad in die Unit schreiben |
| `sudo dredge install --user dredge` | Dienst unter einem eigenen Nutzer laufen lassen |
| `sudo dredge install --jitter 5m` | Start zufällig um bis zu 5 min verzögern |
| `sudo dredge uninstall` | Timer stoppen, deaktivieren, Units löschen |

Der Aufruf ist idempotent: unveränderte Units werden nicht neu geschrieben und
lösen kein `daemon-reload` aus. Nach jeder Zeitplan-Änderung (`dredge settings`)
einfach `sudo dredge install` erneut ausführen.

Kontrolle und Fehlersuche:

```bash
systemctl list-timers dredge.timer   # wann läuft es das nächste Mal?
sudo systemctl start dredge.service  # Lauf sofort auslösen
journalctl -u dredge.service -f      # mitlesen
```

## Konfiguration

Vollständig dokumentiert in [`config.example.yaml`](config.example.yaml). Die
Datei ist von Hand editierbar; `settings` schreibt dieselbe Struktur. Pfadsuche:
`$DREDGE_CONFIG` → `/etc/dredge/config.yaml` → `~/.config/dredge/config.yaml`.

## Telegram einrichten

1. Bot bei **@BotFather** anlegen → Token.
2. Bot in die Zielgruppe/den Chat einladen, Chat-ID besorgen (z. B. via `@RawDataBot`).
3. Im `setup` Token + Chat-ID eintragen.

> Wichtig: `dredge` nutzt `getUpdates` (Long-Polling). Verwende einen
> **eigenen** Bot – auf demselben Token vertragen sich ein gesetzter Webhook
> und Polling nicht, die beiden streiten sich um die Updates.

## Historie

Jeder Lauf wird als JSON-Zeile in `audit.log_file` protokolliert (Zeit, Modus,
entfernte Objekte, freigegebene MB, Fehler). Gut fürs Nachvollziehen und für
spätere Auswertung/Trends.

## Was noch drin steckt / Ideen für später

- Threshold-Trigger (`trigger.min_reclaimable_mb`): still bleiben, wenn eh nichts anliegt.
- Prometheus-Metrik-Endpoint für ein Grafana-/Uptime-Kuma-Dashboard.
- Mehrere Docker-Hosts in einem Lauf.

## Projektstruktur

```
main.go                     Einstieg
cmd/                        CLI (cobra): setup, run, scan, daemon, install …
internal/config             YAML-Config, Defaults, Validierung
internal/docker             schlanker Engine-API-Client (raw HTTP, proxy-freundlich)
internal/janitor            Scan → Plan (Safety-Rails) → Execute
internal/notify             shoutrrr (Multi-Channel) + Telegram-Bot (Buttons)
internal/report             Text-/HTML-Formatierung
internal/audit              JSONL-Historie
internal/runner             Orchestrierung eines kompletten Laufs
internal/systemd            cron → OnCalendar, Units erzeugen/installieren
install.sh                  Installer für fertige Release-Binaries
deploy/systemd              Referenz-Units
Dockerfile, docker-compose.yml, Makefile
```

## Tests

```bash
go test ./...
```

Die Kern-Pipeline (Scan + Safety + Execute) wird gegen einen Mock-Docker-Server
(`httptest`) end-to-end getestet, inkl. Alters- und Label-Schutz sowie Trockenlauf.
Die cron→`OnCalendar`-Übersetzung wird zusätzlich gegen das echte
`systemd-analyze calendar` geprüft, sofern vorhanden.
