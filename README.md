# dredge

[![CI](https://github.com/W0rkingChr1s/dredge/actions/workflows/ci.yml/badge.svg)](https://github.com/W0rkingChr1s/dredge/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Geführtes, sicheres Aufräumen für Docker – als ein einzelnes Go-Binary. Der
Nachbau (und Ausbau) eines n8n-Flows, der wöchentlich ungenutzte Docker-Ressourcen
meldet und per Telegram-Buttons zur Freigabe stellt – nur eben ohne n8n, self-contained.

- **Ein Binary**, keine Runtime-Abhängigkeiten. Läuft als systemd-Timer **oder** als Docker-Container.
- **Geführte Einrichtung** im Terminal-UI (`setup`), Einstellungen jederzeit änderbar (`settings`).
- **Interaktive Freigabe** über Telegram (Inline-Buttons) mit Timeout-Default – plus Mail/ntfy/Gotify/Discord über shoutrrr.
- **Safety-first**: Mindestalter, Schutz-Labels, geschützte Volume-Namen, Trockenlauf.
- **Voller Umfang**: dangling & ungenutzte Images, gestoppte Container, ungenutzte Netzwerke, Build-Cache und (extra abgesicherte) Volumes.

## Schnellstart

```bash
# 1. Bauen (oder fertiges Binary aus dist/ nehmen)
make build            # -> dist/dredge

# 2. Einrichten (interaktiver Assistent)
dredge setup

# 3. Anschauen, was bereinigt würde – ändert nichts
dredge scan

# 4. Kompletter Probelauf ohne Löschen
dredge run --dry-run

# 5. Scharf schalten
dredge run
```

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
| `install-timer` | systemd service + timer aus dem Schedule erzeugen |
| `version` | Version anzeigen |

## Modi pro Ressourcentyp

Jeder Typ (Images, Container, Netzwerke, Build-Cache, Volumes) hat einen Modus:

- `off` – wird nie angefasst
- `auto` – wird ohne Nachfrage bereinigt
- `ask` – löst eine Telegram-Freigabe mit Buttons aus

Bei einem Lauf werden `auto`-Typen direkt eingeplant; für `ask`-Typen kommt eine
Telegram-Nachricht mit Buttons („🧹 Alles / ▶ nur X / ✋ Nichts"). Antwortest du
nicht innerhalb von `approval_timeout_minutes`, greift `on_timeout`
(`none` | `dangling` | `all`).

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
docker compose run --rm dredge setup   # schreibt ./config/config.yaml
# Danach:
docker compose up -d                   # CMD = daemon, läuft nach Schedule
```

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
sudo install -m0755 dist/dredge /usr/local/bin/dredge
sudo mkdir -p /etc/dredge
sudo DREDGE_CONFIG=/etc/dredge/config.yaml dredge setup

# Timer aus dem Schedule erzeugen und aktivieren:
sudo dredge install-timer        # oder: --print zum Ansehen
sudo systemctl daemon-reload
sudo systemctl enable --now dredge.timer
systemctl list-timers dredge.timer
```

`install-timer` übersetzt deinen Cron-Ausdruck automatisch nach `OnCalendar`.

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
- Toggle-Buttons (Mehrfachauswahl) statt Einfachauswahl in Telegram.

## Projektstruktur

```
main.go                     Einstieg
cmd/                        CLI (cobra): setup, run, scan, daemon, install-timer …
internal/config             YAML-Config, Defaults, Validierung
internal/docker             schlanker Engine-API-Client (raw HTTP, proxy-freundlich)
internal/janitor            Scan → Plan (Safety-Rails) → Execute
internal/notify             shoutrrr (Multi-Channel) + Telegram-Bot (Buttons)
internal/report             Text-/HTML-Formatierung
internal/audit              JSONL-Historie
internal/runner             Orchestrierung eines kompletten Laufs
deploy/systemd              Beispiel-Units
Dockerfile, docker-compose.yml, Makefile
```

## Tests

```bash
go test ./...
```

Die Kern-Pipeline (Scan + Safety + Execute) wird gegen einen Mock-Docker-Server
(`httptest`) end-to-end getestet, inkl. Alters- und Label-Schutz sowie Trockenlauf.
