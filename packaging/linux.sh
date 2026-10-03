#!/bin/sh
# Bibli — a Linux version for one PC: Bibli-linux.tar.gz, written to dist/ (or
# the directory given as first argument).
#
# The archive holds a Bibli folder to extract anywhere (Documents, say), with
# the server for amd64 and arm64 and bibli.sh, which picks the right one. The
# first launch of bibli.sh asks for the librarian password and adds Bibli to
# the applications menu; from then on the menu entry is what starts it. It
# starts the server on 127.0.0.1:8765 in the background and opens it in a
# window of its own: Chrome, Edge, Brave or Chromium in app mode, with a
# profile of its own, so no tabs, no address bar, no bookmarks. Closing that
# window stops the server. Without one of those browsers it opens in the
# default one, and a second launch offers to stop it. Data lives in
# ~/.local/share/bibli, apart from the folder, so a new version replaces the
# folder and keeps the library.
#
# Deliberately a single-PC setup, like packaging/macos.sh: localhost only,
# nothing restarts it after a power cut. Dialogs use zenity or kdialog, and
# fall back to the terminal the script was started from.
#
# Needs Go and tar; builds on any system, the release runner included. The
# icon is packaging/icons/bibli.png (icons.sh).

set -eu

cd "$(dirname "$0")/.."
OUT=$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
DIR="$WORK/Bibli"
mkdir -p "$DIR"

VERSION=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always 2>/dev/null || echo dev)
echo "Building Bibli $VERSION for Linux (amd64 and arm64)…"
for arch in amd64 arm64; do
    GOOS=linux GOARCH=$arch CGO_ENABLED=0 \
        go build -trimpath -ldflags="-s -w" -o "$DIR/bibli-$arch" ./app
done

cp packaging/icons/bibli.png "$DIR/"

cat > "$DIR/bibli.sh" <<'EOF'
#!/bin/bash
# Starts Bibli on localhost in the background and opens it in a window of its
# own; closing that window stops the server. Adds Bibli to the applications
# menu on the first launch, and again if this folder has moved.
PORT="${BIBLI_PORT:-8765}"
URL="http://localhost:$PORT/"
HERE="$(cd "$(dirname "$0")" && pwd)"
SHARE="${XDG_DATA_HOME:-$HOME/.local/share}"
DATA="$SHARE/bibli"
mkdir -p "$DATA"

error() {
  if command -v zenity > /dev/null; then zenity --error --title=Bibli --text="$1" 2> /dev/null
  elif command -v kdialog > /dev/null; then kdialog --title Bibli --error "$1"
  else echo "Bibli : $1" >&2; fi
}
notify() {
  if command -v notify-send > /dev/null; then notify-send Bibli "$1"; else echo "$1"; fi
}

case "$(uname -m)" in
  x86_64 | amd64) BIN="$HERE/bibli-amd64" ;;
  aarch64 | arm64) BIN="$HERE/bibli-arm64" ;;
  *) error "Bibli ne fonctionne pas sur ce processeur ($(uname -m))."; exit 1 ;;
esac

running() {
  if command -v curl > /dev/null; then curl -s -o /dev/null --max-time 1 "${URL}healthcheck"
  else wget -q -O /dev/null -T 1 "${URL}healthcheck"; fi
}

PWFILE="$DATA/password"
if [ ! -s "$PWFILE" ]; then
  # Typed twice: it is hidden, and a typo would lock the librarian out.
  ask() {
    if command -v zenity > /dev/null; then zenity --entry --hide-text --title=Bibli --text="$1" 2> /dev/null
    elif command -v kdialog > /dev/null; then kdialog --title Bibli --password "$1"
    elif [ -t 0 ]; then read -rs -p "$1 " answer; echo >&2; printf '%s' "$answer"
    fi
  }
  while :; do
    pw=$(ask "Premier lancement : choisissez le mot de passe de la bibliothèque (il vous sera demandé pour vous connecter).") || exit 0
    [ -n "$pw" ] || exit 0
    pw2=$(ask "Confirmez le mot de passe.") || exit 0
    [ "$pw" = "$pw2" ] && break
    error "Les deux mots de passe ne sont pas identiques."
  done
  (umask 077; printf '%s' "$pw" > "$PWFILE")
fi

# The menu entry points at this folder. Exec is quoted for the spaces a folder
# name may hold; % is the one character the entry format reserves there.
ENTRY="$SHARE/applications/bibli.desktop"
EXEC="\"${HERE//%/%%}/bibli.sh\""
if ! grep -qxF "Exec=$EXEC" "$ENTRY" 2> /dev/null; then
  mkdir -p "$SHARE/applications"
  cat > "$ENTRY" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Bibli
Comment=La bibliothèque
Exec=$EXEC
Icon=$HERE/bibli.png
Terminal=false
Categories=Education;Office;
StartupWMClass=Bibli
DESKTOP
  command -v update-desktop-database > /dev/null && update-desktop-database -q "$SHARE/applications" 2> /dev/null
fi

was_running=0
running && was_running=1
if [ $was_running = 0 ]; then
  # Plain HTTP on localhost: a Secure cookie would be dropped. No one else
  # reaches localhost, so no loans links.
  BIBLI_ADMIN_PASSWORD="$(cat "$PWFILE")" nohup "$BIN" -db "$DATA/biblio.db" \
    -addr "127.0.0.1:$PORT" -secure-cookies=false -tracking-links=false \
    -backup-dir "$DATA/backups" -cache-dir "$DATA/cache" \
    >> "$DATA/bibli.log" 2>&1 &
  for _ in $(seq 1 50); do running && break; sleep 0.2; done
  if ! running; then
    error "Bibli n’a pas pu démarrer. Voir $DATA/bibli.log"
    exit 1
  fi
fi

# A Chromium browser gives a window with no tabs or bars (app mode), whatever
# the default browser is.
BROWSER="${BIBLI_BROWSER:-}"
if [ -z "$BROWSER" ]; then
  for b in google-chrome google-chrome-stable microsoft-edge microsoft-edge-stable brave-browser chromium chromium-browser; do
    if command -v "$b" > /dev/null; then BROWSER=$(command -v "$b"); break; fi
  done
fi
# A snap browser (Ubuntu's Chromium) may only write under ~/snap/<name>.
PROFILE="$DATA/browser"
snap=$(basename "${BROWSER:-none}"); snap=${snap%-browser}
[ -e "/snap/bin/$snap" ] && PROFILE="$HOME/snap/$snap/common/bibli-browser"

if [ -n "$BROWSER" ]; then
  args=(--app="$URL" --user-data-dir="$PROFILE" --class=Bibli --no-first-run
        --no-default-browser-check --disable-background-mode --window-size=1280,860)
  if pgrep -f -- "--user-data-dir=$PROFILE" > /dev/null; then
    # Its window is up: open one more, and let the first launch stop the server.
    "$BROWSER" "${args[@]}" > /dev/null 2>&1 &
  else
    # The server lives as long as the window's browser does. Detached, so the
    # launcher exits and the menu entry can be used again meanwhile.
    nohup bash -c '"$0" "${@:2}"; pkill -f -- "$1 -db"' "$BROWSER" "$BIN" "${args[@]}" > /dev/null 2>&1 &
  fi
  exit 0
fi

# No app-mode browser: the default one, and a second launch offers to stop.
if [ $was_running = 1 ]; then
  if command -v zenity > /dev/null; then
    zenity --question --title=Bibli --text="Bibli est déjà lancé." --ok-label="Arrêter Bibli" --cancel-label=Ouvrir 2> /dev/null
  elif command -v kdialog > /dev/null; then
    kdialog --title Bibli --yes-label "Arrêter Bibli" --no-label Ouvrir --yesno "Bibli est déjà lancé."
  else
    false
  fi && { pkill -f -- "$BIN -db" && notify "Bibli est arrêté."; exit 0; }
fi
xdg-open "$URL" > /dev/null 2>&1 &
EOF
chmod +x "$DIR/bibli.sh" "$DIR"/bibli-*

# tar.gz, not zip: it keeps the executable bits. Without the macOS extras (._
# files, extended attributes), which GNU tar warns about on extraction.
rm -f "$OUT/Bibli-linux.tar.gz"
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$OUT/Bibli-linux.tar.gz" -C "$WORK" Bibli
echo "Written: $OUT/Bibli-linux.tar.gz"
