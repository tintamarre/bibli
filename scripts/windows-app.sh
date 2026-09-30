#!/bin/sh
# Bibli — a Windows version for one PC, built from this machine: Bibli-windows.zip,
# written to dist/ (or the directory given as first argument).
#
# The zip holds a Bibli folder to extract anywhere (Documents, say) and
# "Lancer Bibli.cmd" to double-click once. That first launch asks for the
# librarian password and puts a Bibli shortcut on the Desktop and in the Start
# menu; from then on the shortcut is what starts it. It starts the server on
# 127.0.0.1:8765 hidden, and opens it in an Edge window of its own (app mode,
# its own profile: no tabs, no address bar, no bookmarks) — Edge ships with
# Windows 10 and 11, Chrome is used if it is missing. Closing that window stops
# the server. Data lives in %LOCALAPPDATA%\Bibli, apart from the folder, so a
# new version replaces the folder and keeps the library.
#
# Deliberately a single-PC setup, like scripts/macos-app.sh: localhost only,
# nothing restarts it after a power cut. bibli.exe is not signed, so SmartScreen
# may warn on the first launch ("More info" → "Run anyway").
#
# Needs Go and zip; builds on any system, the release runner included. The
# icon is scripts/icons/bibli.ico (app-icons.sh).

set -eu

cd "$(dirname "$0")/.."
OUT=$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
DIR="$WORK/Bibli"
mkdir -p "$DIR"

VERSION=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always 2>/dev/null || echo dev)
echo "Building Bibli $VERSION for Windows (amd64, which Windows on ARM also runs)…"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$DIR/bibli.exe" ./app

cp scripts/icons/bibli.ico "$DIR/"

cat > "$DIR/bibli.ps1" <<'EOF'
# Starts Bibli on localhost, hidden, and opens it in a browser window of its
# own; closing that window stops the server.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms, System.Drawing

$port = if ($env:BIBLI_PORT) { $env:BIBLI_PORT } else { 8765 }
$url = "http://localhost:$port/"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$bin = Join-Path $here 'bibli.exe'
$data = Join-Path $env:LOCALAPPDATA 'Bibli'
$profileDir = Join-Path $data 'browser'
New-Item -ItemType Directory -Force -Path $data | Out-Null

# Files extracted from a downloaded zip carry the mark of the web: cleared once,
# or Windows asks again at every launch.
Get-ChildItem $here | Unblock-File -ErrorAction SilentlyContinue

function Test-Running {
  try { (Invoke-WebRequest -UseBasicParsing -TimeoutSec 1 "${url}healthcheck").StatusCode -eq 200 } catch { $false }
}
function Show-Error($text) {
  [System.Windows.Forms.MessageBox]::Show($text, 'Bibli', 'OK', 'Error') | Out-Null
}
function Read-Password {
  $form = New-Object System.Windows.Forms.Form -Property @{
    Text = 'Bibli'; Width = 460; Height = 190; StartPosition = 'CenterScreen'
    FormBorderStyle = 'FixedDialog'; MaximizeBox = $false; MinimizeBox = $false; TopMost = $true }
  $label = New-Object System.Windows.Forms.Label -Property @{
    Text = "Premier lancement : choisissez le mot de passe de la bibliothèque (il vous sera demandé pour vous connecter)."
    Left = 12; Top = 12; Width = 420; Height = 40 }
  $box = New-Object System.Windows.Forms.TextBox -Property @{ Left = 12; Top = 60; Width = 420; UseSystemPasswordChar = $true }
  $ok = New-Object System.Windows.Forms.Button -Property @{ Text = 'OK'; Left = 332; Top = 100; Width = 100; DialogResult = 'OK' }
  $form.Controls.AddRange(@($label, $box, $ok))
  $form.AcceptButton = $ok
  if ($form.ShowDialog() -eq 'OK') { return $box.Text }
  return ''
}

$pwFile = Join-Path $data 'password'
if (-not (Test-Path $pwFile) -or -not (Get-Content -Raw $pwFile)) {
  $pw = Read-Password
  if (-not $pw) { exit }
  Set-Content -NoNewline -Encoding UTF8 -Path $pwFile -Value $pw
}

# A shortcut on the Desktop and in the Start menu, pointing at this folder:
# made on the first launch, and made again if the folder has moved.
$shell = New-Object -ComObject WScript.Shell
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  $lnk = $shell.CreateShortcut((Join-Path $folder 'Bibli.lnk'))
  if ($lnk.Arguments -notlike "*$here*") {
    $lnk.TargetPath = (Get-Command powershell.exe).Source
    $lnk.Arguments = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$here\bibli.ps1`""
    $lnk.WorkingDirectory = $here
    $lnk.WindowStyle = 7
    if (Test-Path "$here\bibli.ico") { $lnk.IconLocation = "$here\bibli.ico" }
    $lnk.Save()
  }
}

if (-not (Test-Running)) {
  $env:BIBLI_ADMIN_PASSWORD = Get-Content -Raw $pwFile
  Start-Process -FilePath $bin -WindowStyle Hidden `
    -RedirectStandardError (Join-Path $data 'bibli.log') -RedirectStandardOutput (Join-Path $data 'bibli.out.log') `
    -ArgumentList @('-db', "`"$data\biblio.db`"", '-addr', "127.0.0.1:$port", '-secure-cookies=false', '-family-links=false',
                    '-backup-dir', "`"$data\backups`"", '-cache-dir', "`"$data\cache`"") | Out-Null
  Remove-Item Env:BIBLI_ADMIN_PASSWORD
  for ($i = 0; $i -lt 50 -and -not (Test-Running); $i++) { Start-Sleep -Milliseconds 200 }
  if (-not (Test-Running)) { Show-Error "Bibli n'a pas pu démarrer. Voir $data\bibli.log"; exit 1 }
}

$browser = @(
  "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
  "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
  "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
  "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
  "$env:LOCALAPPDATA\Google\Chrome\Application\chrome.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $browser) { Start-Process $url; exit }

$browserArgs = @("--app=$url", "--user-data-dir=`"$profileDir`"", '--no-first-run',
                 '--no-default-browser-check', '--disable-background-mode', '--window-size=1280,860')
$open = Get-CimInstance Win32_Process -Filter "Name='msedge.exe' OR Name='chrome.exe'" |
  Where-Object { $_.CommandLine -like "*$profileDir*" }
if ($open) {
  # A window is already up: open one more, and let the first launch stop the server.
  Start-Process -FilePath $browser -ArgumentList $browserArgs
  exit
}
$window = Start-Process -FilePath $browser -ArgumentList $browserArgs -PassThru
$window.WaitForExit()
Get-Process bibli -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $bin } | Stop-Process -Force
EOF

# Double-clicked once, from the extracted folder; the shortcuts take over.
printf '@echo off\r\nstart "" powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%%~dp0bibli.ps1"\r\n' \
    > "$DIR/Lancer Bibli.cmd"

# Windows PowerShell 5 reads a script without a BOM as the ANSI code page, and
# the accents in the dialogs would come out garbled.
printf '\357\273\277' | cat - "$DIR/bibli.ps1" > "$WORK/bibli.ps1" && mv "$WORK/bibli.ps1" "$DIR/bibli.ps1"
# CRLF throughout, so Notepad shows the script as a script.
sed 's/$/\r/' "$DIR/bibli.ps1" > "$WORK/crlf" && mv "$WORK/crlf" "$DIR/bibli.ps1"

rm -f "$OUT/Bibli-windows.zip"
(cd "$WORK" && zip -qr "$OUT/Bibli-windows.zip" Bibli)
echo "Written: $OUT/Bibli-windows.zip"
