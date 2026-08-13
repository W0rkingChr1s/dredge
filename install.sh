#!/bin/sh
# dredge-Installer – lädt ein fertiges Release-Binary, prüft die Checksumme
# und legt es nach /usr/local/bin. Go wird nicht benötigt.
#
#   curl -fsSL https://raw.githubusercontent.com/W0rkingChr1s/dredge/main/install.sh | sh
#
# Umgebungsvariablen:
#   DREDGE_VERSION=v0.1.0   bestimmte Version statt der neuesten
#   PREFIX=$HOME/.local     Zielpräfix (Binary landet in $PREFIX/bin)

set -eu

REPO="W0rkingChr1s/dredge"
BIN="dredge"
PREFIX="${PREFIX:-/usr/local}"
VERSION="${DREDGE_VERSION:-latest}"

usage() {
	cat <<EOF
dredge-Installer

  --version <tag>   Version installieren, z. B. v0.1.0 (Standard: neueste)
  --prefix <pfad>   Zielpräfix, Binary landet in <pfad>/bin (Standard: /usr/local)
  --help            diese Hilfe
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="${2:?--version braucht einen Wert}"; shift 2 ;;
	--prefix) PREFIX="${2:?--prefix braucht einen Wert}"; shift 2 ;;
	--help | -h) usage; exit 0 ;;
	*) echo "Unbekannte Option: $1" >&2; usage >&2; exit 2 ;;
	esac
done

die() {
	echo "Fehler: $*" >&2
	exit 1
}

# ---- Umgebung prüfen -------------------------------------------------------

[ "$(uname -s)" = "Linux" ] || die "dredge-Releases gibt es nur für Linux (hier: $(uname -s))."

case "$(uname -m)" in
x86_64 | amd64) ARCH="amd64" ;;
aarch64 | arm64) ARCH="arm64" ;;
armv7l | armv6l | armhf | arm) ARCH="armv7" ;;
*) die "Architektur $(uname -m) wird nicht unterstützt (amd64, arm64, armv7)." ;;
esac
ASSET="$BIN-linux-$ARCH"

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	die "Weder curl noch wget gefunden."
fi

if command -v sha256sum >/dev/null 2>&1; then
	checksum() { sha256sum -c -; }
elif command -v shasum >/dev/null 2>&1; then
	checksum() { shasum -a 256 -c -; }
else
	die "Weder sha256sum noch shasum gefunden – ohne Prüfsumme wird nicht installiert."
fi

BINDIR="$PREFIX/bin"
if [ "$(id -u)" -eq 0 ]; then
	SUDO=""
elif [ -w "$PREFIX" ] || { [ -d "$BINDIR" ] && [ -w "$BINDIR" ]; }; then
	SUDO=""
elif command -v sudo >/dev/null 2>&1; then
	SUDO="sudo"
	echo "Schreiben nach $BINDIR braucht root – benutze sudo."
else
	die "Kein Schreibrecht auf $BINDIR und kein sudo. Alternative: PREFIX=\$HOME/.local sh install.sh"
fi

# ---- Version auflösen ------------------------------------------------------

if [ "$VERSION" = "latest" ]; then
	echo "Suche neuestes Release …"
	VERSION="$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
	[ -n "$VERSION" ] || die "Konnte das neueste Release nicht ermitteln – setze DREDGE_VERSION=vX.Y.Z."
fi

BASE="https://github.com/$REPO/releases/download/$VERSION"
echo "Installiere dredge $VERSION ($ARCH) nach $BINDIR …"

# ---- Herunterladen und prüfen ---------------------------------------------

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

fetch "$BASE/$ASSET" "$TMP/$ASSET" ||
	die "Download fehlgeschlagen: $BASE/$ASSET (gibt es die Version und die Architektur?)"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" ||
	die "SHA256SUMS nicht abrufbar – Abbruch, es wird nichts ungeprüft installiert."

(
	cd "$TMP"
	grep "[[:space:]]$ASSET\$" SHA256SUMS >expected.txt
	checksum <expected.txt >/dev/null
) || die "Prüfsumme für $ASSET fehlt oder stimmt nicht – Download verworfen."

chmod 0755 "$TMP/$ASSET"
"$TMP/$ASSET" version >/dev/null || die "Das heruntergeladene Binary lässt sich nicht ausführen."

# ---- Installieren ----------------------------------------------------------

$SUDO install -d "$BINDIR"
$SUDO install -m0755 "$TMP/$ASSET" "$BINDIR/$BIN"

echo "✓ $($BINDIR/$BIN version) installiert nach $BINDIR/$BIN"

case ":$PATH:" in
*":$BINDIR:"*) ;;
*) echo "⚠  $BINDIR liegt nicht in \$PATH – ergänze es in deiner Shell-Konfiguration." ;;
esac

cat <<EOF

Weiter geht's:
  sudo mkdir -p /etc/dredge
  sudo $BIN setup      # geführte Einrichtung, schreibt /etc/dredge/config.yaml
  sudo $BIN scan       # anzeigen, was bereinigt würde
  sudo $BIN install    # als systemd-Timer einplanen
EOF
