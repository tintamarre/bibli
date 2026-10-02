#!/bin/sh
# Bibli — draws the icons from app/static/favicon.svg. The desktop icons go to
# scripts/icons/: Bibli.icns for the Mac app and bibli.png for the Linux menu
# entry (the favicon on a light rounded square). The Windows ones go to
# app/winsetup/, embedded in bibli.exe: bibli.ico for the shortcuts, and
# bibli-tray.ico for the Windows service tray (the book mark alone,
# full bleed on transparency, so it reads at 16 px on any taskbar). The web
# home-screen icons go to app/static/ (icon-192, icon-512 and apple-touch-icon.png,
# the logo on a solid white square).
#
# Run on a Mac, and only when the logo changes: the result is committed, so
# that macos-app.sh, windows.sh and linux-app.sh build
# anywhere, the release runner included, without a browser to draw with. Needs Google Chrome, sips,
# iconutil and python3.

set -eu

cd "$(dirname "$0")/.."
OUT=scripts/icons
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}

cp app/static/favicon.svg "$WORK/"
cat > "$WORK/icon.html" <<'EOF'
<html><body style="margin:0;background:transparent">
<div style="width:824px;height:824px;margin:100px;border-radius:185px;background:linear-gradient(#ffffff,#e8eefc);box-shadow:0 10px 30px rgba(0,0,0,.25);display:flex;align-items:center;justify-content:center">
<img src="favicon.svg" style="width:600px;height:600px"></div></body></html>
EOF
(cd "$WORK" && "$CHROME" --headless=new --disable-gpu --default-background-color=00000000 \
    --screenshot="$WORK/icon1024.png" --window-size=1024,1024 --hide-scrollbars icon.html 2>/dev/null)

mkdir -p "$OUT" "$WORK/Bibli.iconset"
for s in 16 32 128 256 512; do
    sips -z $s $s "$WORK/icon1024.png" --out "$WORK/Bibli.iconset/icon_${s}x${s}.png" > /dev/null
    sips -z $((s * 2)) $((s * 2)) "$WORK/icon1024.png" --out "$WORK/Bibli.iconset/icon_${s}x${s}@2x.png" > /dev/null
done
iconutil -c icns "$WORK/Bibli.iconset" -o "$OUT/Bibli.icns"

# An .ico of PNG images, which every Windows since Vista reads.
for s in 16 32 48 256; do
    sips -z $s $s "$WORK/icon1024.png" --out "$WORK/ico$s.png" > /dev/null
done
python3 - "$WORK" app/winsetup/bibli.ico <<'EOF'
import struct, sys
work, out = sys.argv[1], sys.argv[2]
sizes = [16, 32, 48, 256]
images = [open(f"{work}/ico{s}.png", "rb").read() for s in sizes]
data = struct.pack("<HHH", 0, 1, len(sizes))
offset = 6 + 16 * len(sizes)
for s, img in zip(sizes, images):
    data += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(img), offset)
    offset += len(img)
open(out, "wb").write(data + b"".join(images))
EOF
cp "$WORK/ico256.png" "$OUT/bibli.png"

# A tray icon for the Windows service: the book mark alone (no sparks, no light
# panel), full bleed on transparency, in a brighter blue so it reads on both
# light and dark taskbars. Small sizes for DPI scaling (100/125/150/200 %).
cat > "$WORK/tray.svg" <<'EOF'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="2 5.5 20 20">
 <g fill="none" stroke="#3b82f6" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
  <path d="M3 10.2A2.2 2.2 0 0 1 5.2 8H10a2 2 0 0 1 2 2 2 2 0 0 1 2-2h4.8A2.2 2.2 0 0 1 21 10.2V17.8A2.2 2.2 0 0 1 18.8 20H14a2 2 0 0 0-2 2 2 2 0 0 0-2-2H5.2A2.2 2.2 0 0 1 3 17.8z"/>
  <path d="M12 10v11.5"/>
 </g>
</svg>
EOF
cat > "$WORK/tray.html" <<'EOF'
<html><body style="margin:0;background:transparent">
<img src="tray.svg" style="width:512px;height:512px"></body></html>
EOF
(cd "$WORK" && "$CHROME" --headless=new --disable-gpu --default-background-color=00000000 \
    --screenshot="$WORK/tray512.png" --window-size=512,512 --hide-scrollbars tray.html 2>/dev/null)
for s in 16 20 24 32; do
    sips -z $s $s "$WORK/tray512.png" --out "$WORK/tray$s.png" > /dev/null
done
python3 - "$WORK" app/winsetup/bibli-tray.ico <<'EOF'
import struct, sys
work, out = sys.argv[1], sys.argv[2]
sizes = [16, 20, 24, 32]
images = [open(f"{work}/tray{s}.png", "rb").read() for s in sizes]
data = struct.pack("<HHH", 0, 1, len(sizes))
offset = 6 + 16 * len(sizes)
for s, img in zip(sizes, images):
    data += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(img), offset)
    offset += len(img)
open(out, "wb").write(data + b"".join(images))
EOF

# The web home-screen icons (Add to Home Screen on a phone). Full bleed on a
# light gradient, like the desktop icon, with the logo kept inside the maskable
# safe zone (~55% of the width) so Android can shape it into its adaptive icon
# without clipping the logo, and iOS can round it. Declared "maskable" in the web
# app manifest, and linked as the apple-touch-icon. Served from app/static/.
cat > "$WORK/web.html" <<'EOF'
<html><body style="margin:0">
<div style="width:1024px;height:1024px;background:linear-gradient(#ffffff,#e6ecfb);display:flex;align-items:center;justify-content:center">
<img src="favicon.svg" style="width:560px;height:560px"></div></body></html>
EOF
(cd "$WORK" && "$CHROME" --headless=new --disable-gpu \
    --screenshot="$WORK/web1024.png" --window-size=1024,1024 --hide-scrollbars web.html 2>/dev/null)
sips -z 192 192 "$WORK/web1024.png" --out app/static/icon-192.png > /dev/null
sips -z 512 512 "$WORK/web1024.png" --out app/static/icon-512.png > /dev/null
sips -z 180 180 "$WORK/web1024.png" --out app/static/apple-touch-icon.png > /dev/null

echo "Written: $OUT/Bibli.icns, $OUT/bibli.png, app/winsetup/{bibli,bibli-tray}.ico and app/static/{icon-192,icon-512,apple-touch-icon}.png"
