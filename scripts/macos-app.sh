#!/bin/sh
# Bibli — a macOS app for one Mac: Bibli.app, Bibli-macos.zip holding it, and
# on a Mac a Bibli.dmg to drag it into Applications, written to dist/ (or the
# directory given as first argument). The release job runs it on Linux.
#
# Double-clicking the app starts the server on 127.0.0.1:8765 in the background
# and opens it in a window of its own: Chrome, Edge, Brave or Chromium in app
# mode, with a profile of its own, so no tabs, no address bar, no bookmarks.
# Quitting that window (Cmd+Q) stops the server. Without one of those browsers
# it opens in the default one, and opening the app again offers to stop it. Data
# lives in ~/Library/Application Support/Bibli, apart from the app, so a new
# version replaces the app and keeps the library. The first launch asks for the
# librarian password and keeps it there, readable by the user alone.
#
# Deliberately a single-Mac setup, not a school deployment (README): it listens
# on localhost only, so no tablet reaches it, and nothing restarts it after a
# power cut. Nothing is signed by Apple: on another Mac, the first launch is
# right-click → Open, or "Open Anyway" under Privacy & Security.
#
# Needs Go, zip and, off a Mac, python3 — lipo, codesign and hdiutil are used
# when they are there. The icon is scripts/icons/Bibli.icns (app-icons.sh).

set -eu

cd "$(dirname "$0")/.."
OUT=$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

VERSION=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always 2>/dev/null || echo dev)
SHORT=$(echo "$VERSION" | sed -E 's/^v?([0-9]+(\.[0-9]+)*).*/\1/')
case $SHORT in [0-9]*) ;; *) SHORT=0 ;; esac

echo "Building Bibli $VERSION for arm64 and amd64…"
for arch in arm64 amd64; do
    GOOS=darwin GOARCH=$arch CGO_ENABLED=0 \
        go build -trimpath -ldflags="-s -w" -o "$WORK/bibli-$arch" ./app
done

APP="$WORK/Bibli.app/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources"
if command -v lipo > /dev/null; then
    lipo -create -output "$APP/Resources/bibli" "$WORK/bibli-arm64" "$WORK/bibli-amd64"
else
    # A universal binary is a table of contents in front of the two slices,
    # each aligned to 16 KiB; lipo is only on a Mac. Go signs the arm64 slice
    # itself, which is the signature Apple Silicon insists on.
    python3 - "$APP/Resources/bibli" "$WORK/bibli-arm64" "$WORK/bibli-amd64" <<'EOF'
import struct, sys
out, *paths = sys.argv[1:]
slices = [open(p, "rb").read() for p in paths]
align = 14
header = struct.pack(">II", 0xCAFEBABE, len(slices))
offset, entries, body = 1 << align, b"", b""
for data in slices:
    cputype, cpusubtype = struct.unpack("<ii", data[4:12])
    entries += struct.pack(">iiIII", cputype, cpusubtype, offset, len(data), align)
    body += data + b"\0" * (-len(data) % (1 << align))
    offset += len(data) + (-len(data) % (1 << align))
head = header + entries
open(out, "wb").write(head + b"\0" * ((1 << align) - len(head)) + body)
EOF
    chmod +x "$APP/Resources/bibli"
fi
cp scripts/icons/Bibli.icns "$APP/Resources/"

cat > "$APP/MacOS/Bibli" <<'EOF'
#!/bin/bash
# Starts Bibli on localhost in the background and opens it in a window.
PORT="${BIBLI_PORT:-8765}"
URL="http://localhost:$PORT/"
DATA="$HOME/Library/Application Support/Bibli"
BIN="$(cd "$(dirname "$0")/../Resources" && pwd)/bibli"
PROFILE="$DATA/browser"
mkdir -p "$DATA"

running() { curl -s -o /dev/null --max-time 1 "${URL}healthcheck"; }

# A Chromium browser gives a window with no tabs or bars (app mode), whatever
# the default browser is.
BROWSER="${BIBLI_BROWSER:-}"
if [ -z "$BROWSER" ]; then
  for b in "Google Chrome" "Microsoft Edge" "Brave Browser" "Chromium"; do
    for dir in /Applications "$HOME/Applications"; do
      if [ -x "$dir/$b.app/Contents/MacOS/$b" ]; then
        BROWSER="$dir/$b.app/Contents/MacOS/$b"
        break 2
      fi
    done
  done
fi

PWFILE="$DATA/password"
if [ ! -s "$PWFILE" ]; then
  pw=$(osascript -e 'text returned of (display dialog "Premier lancement : choisissez le mot de passe de la bibliothèque (il vous sera demandé pour vous connecter)." default answer "" with hidden answer with title "Bibli")') || exit 0
  [ -n "$pw" ] || exit 0
  umask 077; printf '%s' "$pw" > "$PWFILE"
fi

was_running=0
running && was_running=1
if [ $was_running = 0 ]; then
  # Plain HTTP on localhost: a Secure cookie would be dropped by Safari. No
  # family reaches localhost, so no loans links.
  BIBLI_ADMIN_PASSWORD="$(cat "$PWFILE")" nohup "$BIN" -db "$DATA/biblio.db" \
    -addr "127.0.0.1:$PORT" -secure-cookies=false -family-links=false \
    -backup-dir "$DATA/backups" -cache-dir "$DATA/cache" \
    >> "$DATA/bibli.log" 2>&1 &
  for _ in $(seq 1 50); do running && break; sleep 0.2; done
  if ! running; then
    osascript -e 'display dialog "Bibli n’a pas pu démarrer. Voir ~/Library/Application Support/Bibli/bibli.log" buttons {"OK"} with icon stop with title "Bibli"'
    exit 1
  fi
fi

if [ -n "$BROWSER" ]; then
  args=(--app="$URL" --user-data-dir="$PROFILE" --no-first-run --no-default-browser-check --window-size=1280,860)
  if pgrep -f -- "--user-data-dir=$PROFILE" > /dev/null; then
    # Its window is up, or was closed without quitting: open one more.
    "$BROWSER" "${args[@]}" > /dev/null 2>&1
  else
    # The server lives as long as the window's browser does. Detached, so the
    # launcher exits and the app can be opened again meanwhile.
    nohup bash -c '"$0" "${@:2}"; pkill -f "$1 -db"' "$BROWSER" "$BIN" "${args[@]}" > /dev/null 2>&1 &
  fi
  exit 0
fi

# No app-mode browser: the default one, and a second launch offers to stop.
if [ $was_running = 1 ]; then
  choice=$(osascript -e 'button returned of (display dialog "Bibli est déjà lancé." buttons {"Arrêter Bibli", "Ouvrir"} default button "Ouvrir" with title "Bibli")') || exit 0
  if [ "$choice" = "Arrêter Bibli" ]; then
    pkill -f "$BIN -db" && osascript -e 'display notification "Bibli est arrêté." with title "Bibli"'
    exit 0
  fi
fi
open "$URL"
EOF
chmod +x "$APP/MacOS/Bibli"

cat > "$APP/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Bibli</string>
  <key>CFBundleDisplayName</key><string>Bibli</string>
  <key>CFBundleIdentifier</key><string>io.github.tintamarre.bibli</string>
  <key>CFBundleExecutable</key><string>Bibli</string>
  <key>CFBundleIconFile</key><string>Bibli</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$SHORT</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
</dict></plist>
EOF

command -v codesign > /dev/null && codesign --force --deep -s - "$WORK/Bibli.app"

rm -rf "$OUT/Bibli.app" "$OUT/Bibli.dmg" "$OUT/Bibli-macos.zip"
# zip keeps the execute bits, which the Finder honours when it unpacks.
(cd "$WORK" && zip -qry "$OUT/Bibli-macos.zip" Bibli.app)
if command -v hdiutil > /dev/null; then
    mkdir "$WORK/dmg"
    cp -R "$WORK/Bibli.app" "$WORK/dmg/"
    ln -s /Applications "$WORK/dmg/Applications"
    hdiutil create -quiet -volname Bibli -srcfolder "$WORK/dmg" -ov -format UDZO "$OUT/Bibli.dmg"
fi
mv "$WORK/Bibli.app" "$OUT/"
for f in Bibli.app Bibli-macos.zip Bibli.dmg; do
    [ -e "$OUT/$f" ] && echo "Written: $OUT/$f"
done
true
