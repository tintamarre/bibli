#!/bin/sh
# Bibli — development server that always runs the code in the working tree.
#
# Rebuilds and restarts as soon as a source file changes: the Go sources under
# app/, but also its templates, CSS, JS and migrations, since all of them are
# baked into the binary by embed.FS. Editing a template without a rebuild would keep showing the old
# one, which is exactly the trap this script exists to remove.
#
# No dependency: change detection is `find -newer` against a stamp file, polled
# once a second. A watcher such as entr or fswatch would react faster, but it
# would be one more thing to install on a machine that has to stay reproducible
# years from now.
#
# A failed build never takes the running server down: the binary is compiled to
# a side file first, and the old process is only replaced once it compiles.

set -eu

# Colour only when this script writes to a terminal: escape codes in a piped or
# redirected log are noise. The server makes the same check on its own stderr
# before colouring its request log, so BIBLI_LOG_COLOR is an offer, not an order.
if [ -t 1 ]; then
    C_BOLD=$(printf '\033[1m')
    C_DIM=$(printf '\033[2m')
    C_RED=$(printf '\033[31m')
    C_OFF=$(printf '\033[0m')
else
    C_BOLD='' C_DIM='' C_RED='' C_OFF=''
fi

ADDR="${BIBLI_DEV_ADDR:-127.0.0.1:8080}"
DB="${BIBLI_DEV_DB:-data/biblio.db}"
PASSWORD="${BIBLI_ADMIN_PASSWORD:-dev}"
# A fresh checkout has no data/ yet: SQLite will not create the directory.
mkdir -p "$(dirname "$DB")"
BIN="${TMPDIR:-/tmp}/bibli-dev-$$"
STAMP="${TMPDIR:-/tmp}/bibli-dev-stamp-$$"
PID=""

# Watched roots. `data` is deliberately absent: the database changes on every
# page view and would restart the server constantly.
ROOTS="go.mod go.sum app scripts"

cleanup() {
    stop_server
    rm -f "$BIN" "$BIN.new" "$STAMP"
    exit 0
}

# Compile beside the running binary. Replacing the file in place would fail
# with "text file busy" on Linux while the old server still executes it.
build() {
    go build -o "$BIN.new" ./app || return 1
    mv "$BIN.new" "$BIN"
}

stop_server() {
    if [ -n "$PID" ]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
        PID=""
    fi
}

start_server() {
    BIBLI_ADMIN_PASSWORD="$PASSWORD" BIBLI_LOG_COLOR=1 "$BIN" \
        -db "$DB" -addr "$ADDR" \
        -backup-dir data/backups -cache-dir data/cache \
        -secure-cookies=false &
    PID=$!
}

reload() {
    if build; then
        stop_server
        start_server
    elif [ -n "$PID" ]; then
        echo "$C_RED--- build failed: the previous version keeps running ---$C_OFF"
    else
        echo "$C_RED--- build failed: nothing to serve yet ---$C_OFF"
    fi
}

trap cleanup INT TERM

echo "${C_BOLD}Bibli dev server$C_OFF"
echo "  http://$ADDR   password: $PASSWORD"
echo "$C_DIM  database: $DB"
echo "  rebuilds on any change to: $ROOTS"
echo "  Ctrl-C to stop$C_OFF"
echo

touch "$STAMP"
reload

while true; do
    sleep 1
    # -quit stops at the first match, so each tick stays cheap.
    if [ -n "$(find $ROOTS -type f -newer "$STAMP" -print -quit 2>/dev/null)" ]; then
        touch "$STAMP"
        echo
        echo "$C_BOLD--- change detected, rebuilding ---$C_OFF"
        reload
    fi
done
