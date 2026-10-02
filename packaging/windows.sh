#!/bin/sh
# Bibli for Windows, built from this machine: Bibli-windows.exe, written to
# dist/ (or the directory given as first argument).
#
# The whole download is bibli.exe. Double-clicked, it asks what the PC is for
# (app/setup_windows.go runs packaging/windows/install.ps1) and installs one of:
# - the desktop app, for a single PC: no administrator needed, localhost only,
#   it runs while its Edge window is open (app.ps1);
# - the server for a whole school: a Windows service that starts with the PC
#   and serves every device on the LAN, with a tray icon and an admin menu;
# - on another computer, a Desktop shortcut to that server.
# Double-clicking a newer one updates what is installed.
#
# bibli.exe is not signed: SmartScreen may warn on the first launch.
#
# Needs Go; builds on any system, the release runner included.

set -eu

cd "$(dirname "$0")/.."
OUT=$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)

VERSION=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always 2>/dev/null || echo dev)
echo "Building Bibli $VERSION for Windows (amd64, which Windows on ARM also runs)…"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$OUT/Bibli-windows.exe" ./app
echo "Written: $OUT/Bibli-windows.exe"
