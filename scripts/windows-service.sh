#!/bin/sh
# Bibli — a Windows *server* for a whole school, built from this machine:
# Bibli-windows-server.zip, written to dist/ (or the directory given as first
# argument).
#
# Where scripts/windows-app.sh makes a single-PC app that runs only while its
# window is open, this makes an unattended background server for several
# devices on the school LAN (desk PCs, tablets, USB scanners, ISBN typed in).
#
# The zip holds a Bibli-serveur folder to extract anywhere and a double-click
# "Installer Bibli (serveur).cmd". That installer (it asks for the UAC prompt
# once) copies bibli.exe to Program Files, keeps the database in ProgramData,
# asks for the librarian password once, and registers bibli.exe as a Windows
# service: it starts with the PC as LOCAL SERVICE, Windows restarts it after a
# crash, and a stop or shutdown closes the database cleanly (service_windows.go).
# It opens TCP 8080 on the private firewall profile, and drops a "Bibli" icon, a
# system-tray icon and a "Bibli (serveur)" admin menu. HTTP only, so
# -secure-cookies=false; tablet cameras would need HTTPS.
#
# install.ps1, update.ps1 and uninstall.ps1 take -Unattended (no dialog, no
# pause; the password comes from BIBLI_ADMIN_PASSWORD), which the Windows job
# of .github/workflows/ci.yml uses to test the whole cycle.
#
# The few strings a volunteer sees are French, Dutch or English, following the
# Windows display language (lang.ps1); code, comments and bibli.exe's own logs
# stay English, as everywhere. Same BOM + CRLF treatment as windows-app.sh so
# Windows PowerShell 5 reads the accents correctly.
#
# Needs Go and zip; builds on any system, the release runner included. Icons are
# scripts/icons/bibli.ico (shortcut) and bibli-tray.ico (tray) — app-icons.sh.

set -eu

cd "$(dirname "$0")/.."
OUT=$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
DIR="$WORK/Bibli-serveur"
mkdir -p "$DIR"

VERSION=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always 2>/dev/null || echo dev)
echo "Building Bibli $VERSION server for Windows (amd64, which Windows on ARM also runs)…"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$DIR/bibli.exe" ./app

cp scripts/icons/bibli.ico "$DIR/"
cp scripts/icons/bibli-tray.ico "$DIR/"

# --- lang.ps1: the few user-facing strings, in fr / nl / en ---
cat > "$DIR/lang.ps1" <<'PS1'
# Shared UI strings for the Bibli server scripts. The language follows the
# Windows display language (Get-UICulture); anything other than nl or en falls
# back to French, the reference (CLAUDE.md), suited to FWB schools. Only strings
# a volunteer sees are translated — code, comments and bibli.exe's logs stay
# English. Single-quoted here, so an apostrophe is doubled ('').
function Get-BibliStrings {
  $lang = (Get-UICulture).TwoLetterISOLanguageName
  if ($lang -ne 'nl' -and $lang -ne 'en') { $lang = 'fr' }
  $all = @{
    fr = @{
      done_enter   = 'Terminé. Appuyez sur Entrée pour fermer'
      press_enter  = 'Appuyez sur Entrée pour fermer…'
      yes = 'oui'; no = 'non'
      pw_prompt    = 'Choisissez le mot de passe de la bibliothèque (au moins 12 caractères ; une phrase de trois ou quatre mots convient). Il sera demandé pour se connecter.'
      pw_tooshort  = 'Au moins 12 caractères.'
      installing   = 'Installation de Bibli dans {0} (données dans {1})…'
      cancelled    = 'Annulé.'
      sum_running  = 'Bibli est installé et démarre maintenant à chaque allumage du PC.'
      sum_thispc   = '  Sur ce PC            : {0}'
      sum_others   = '  Tablettes, autres PC : {0}'
      sum_icons1   = 'Icône « Bibli » sur le Bureau, icône dans la zone de notification, et'
      sum_icons2   = 'menu « Bibli (serveur) » dans Démarrer (démarrer / arrêter / mettre à jour).'
      sum_notup    = 'Bibli n''a pas encore répondu. Voir {0}'
      menu_folder  = 'Bibli (serveur)'
      svc_desc     = 'Serveur de la bibliothèque (Bibli).'
      sc_open = 'Ouvrir Bibli'; sc_address = 'Adresse pour les tablettes'; sc_status = 'État du serveur'
      sc_start = 'Démarrer le serveur'; sc_stop = 'Arrêter le serveur'; sc_restart = 'Redémarrer le serveur'
      sc_logs = 'Voir le journal'; sc_data = 'Dossier des données'; sc_update = 'Mettre à jour Bibli'; sc_uninstall = 'Désinstaller Bibli'
      msg_started = 'Serveur démarré.'; msg_stopped = 'Serveur arrêté.'; msg_restarted = 'Serveur redémarré.'
      lbl_task = 'Service :'; lbl_responds = 'Répond sur le réseau :'; lbl_address = 'Adresse :'
      addr_intro = 'Sur les tablettes et les autres ordinateurs, ouvrir :'
      t_hide = 'Cacher l''icône (le serveur continue)'
      tip_up = 'Bibli — en marche'; tip_down = 'Bibli — arrêté'
      up_title = 'Choisir le nouveau bibli.exe ou le fichier .zip téléchargé'
      up_allfiles = 'Tous les fichiers'; up_err_zip = 'bibli.exe introuvable dans le .zip.'; up_err = 'bibli.exe introuvable.'
      up_done = 'Mis à jour. Sauvegarde avant mise à jour : {0}'
      un_kept = 'Bibli a été supprimé. Les données de la bibliothèque sont conservées dans {0}'
      un_all = 'Bibli et toutes ses données ont été supprimés.'
      cl_prompt = 'Nom ou adresse IP du serveur Bibli (ex. BIBLIO-PC)'
      cl_done = 'Raccourci « Bibli » créé sur le Bureau. Il ouvre {0}'
    }
    nl = @{
      done_enter   = 'Klaar. Druk op Enter om te sluiten'
      press_enter  = 'Druk op Enter om te sluiten…'
      yes = 'ja'; no = 'nee'
      pw_prompt    = 'Kies het wachtwoord van de bibliotheek (minstens 12 tekens; een zin van drie of vier woorden volstaat). Het wordt gevraagd om aan te melden.'
      pw_tooshort  = 'Minstens 12 tekens.'
      installing   = 'Bibli wordt geïnstalleerd in {0} (gegevens in {1})…'
      cancelled    = 'Geannuleerd.'
      sum_running  = 'Bibli is geïnstalleerd en start voortaan bij elke start van de pc.'
      sum_thispc   = '  Op deze pc           : {0}'
      sum_others   = '  Tablets, andere pc''s : {0}'
      sum_icons1   = 'Pictogram « Bibli » op het bureaublad, pictogram in het systeemvak, en'
      sum_icons2   = 'menu « Bibli (server) » in Start (starten / stoppen / bijwerken).'
      sum_notup    = 'Bibli heeft nog niet geantwoord. Zie {0}'
      menu_folder  = 'Bibli (server)'
      svc_desc     = 'Server van de bibliotheek (Bibli).'
      sc_open = 'Bibli openen'; sc_address = 'Adres voor tablets'; sc_status = 'Status van de server'
      sc_start = 'Server starten'; sc_stop = 'Server stoppen'; sc_restart = 'Server herstarten'
      sc_logs = 'Logboek bekijken'; sc_data = 'Gegevensmap'; sc_update = 'Bibli bijwerken'; sc_uninstall = 'Bibli verwijderen'
      msg_started = 'Server gestart.'; msg_stopped = 'Server gestopt.'; msg_restarted = 'Server herstart.'
      lbl_task = 'Dienst:'; lbl_responds = 'Antwoordt op het netwerk:'; lbl_address = 'Adres:'
      addr_intro = 'Open op de tablets en de andere computers:'
      t_hide = 'Pictogram verbergen (server blijft draaien)'
      tip_up = 'Bibli — actief'; tip_down = 'Bibli — gestopt'
      up_title = 'Kies de nieuwe bibli.exe of het gedownloade .zip-bestand'
      up_allfiles = 'Alle bestanden'; up_err_zip = 'bibli.exe niet gevonden in de .zip.'; up_err = 'bibli.exe niet gevonden.'
      up_done = 'Bijgewerkt. Back-up vóór de update: {0}'
      un_kept = 'Bibli is verwijderd. De bibliotheekgegevens blijven bewaard in {0}'
      un_all = 'Bibli en al zijn gegevens zijn verwijderd.'
      cl_prompt = 'Naam of IP-adres van de Bibli-server (bv. BIBLIO-PC)'
      cl_done = 'Snelkoppeling « Bibli » op het bureaublad gemaakt. Opent {0}'
    }
    en = @{
      done_enter   = 'Done. Press Enter to close'
      press_enter  = 'Press Enter to close…'
      yes = 'yes'; no = 'no'
      pw_prompt    = 'Choose the library password (at least 12 characters; a phrase of three or four words works well). It will be asked to log in.'
      pw_tooshort  = 'At least 12 characters.'
      installing   = 'Installing Bibli to {0} (data in {1})…'
      cancelled    = 'Cancelled.'
      sum_running  = 'Bibli is installed and now starts every time the PC powers on.'
      sum_thispc   = '  On this PC           : {0}'
      sum_others   = '  Tablets, other PCs   : {0}'
      sum_icons1   = 'A "Bibli" icon on the Desktop, an icon in the notification area, and'
      sum_icons2   = 'a "Bibli (server)" menu in Start (start / stop / update).'
      sum_notup    = 'Bibli has not answered yet. See {0}'
      menu_folder  = 'Bibli (server)'
      svc_desc     = 'Library server (Bibli).'
      sc_open = 'Open Bibli'; sc_address = 'Address for tablets'; sc_status = 'Server status'
      sc_start = 'Start the server'; sc_stop = 'Stop the server'; sc_restart = 'Restart the server'
      sc_logs = 'View the log'; sc_data = 'Data folder'; sc_update = 'Update Bibli'; sc_uninstall = 'Uninstall Bibli'
      msg_started = 'Server started.'; msg_stopped = 'Server stopped.'; msg_restarted = 'Server restarted.'
      lbl_task = 'Service:'; lbl_responds = 'Answers on the network:'; lbl_address = 'Address:'
      addr_intro = 'On the tablets and the other computers, open:'
      t_hide = 'Hide the icon (the server keeps running)'
      tip_up = 'Bibli — running'; tip_down = 'Bibli — stopped'
      up_title = 'Choose the new bibli.exe or the downloaded .zip file'
      up_allfiles = 'All files'; up_err_zip = 'bibli.exe not found in the .zip.'; up_err = 'bibli.exe not found.'
      up_done = 'Updated. Pre-update backup: {0}'
      un_kept = 'Bibli has been removed. The library data is kept in {0}'
      un_all = 'Bibli and all its data have been removed.'
      cl_prompt = 'Name or IP address of the Bibli server (e.g. BIBLIO-PC)'
      cl_done = '"Bibli" shortcut created on the Desktop. It opens {0}'
    }
  }
  return $all[$lang]
}
PS1

# --- install.ps1: copy files, set the password, register the service, shortcuts ---
cat > "$DIR/install.ps1" <<'PS1'
# Installs Bibli as a Windows service that starts with the PC. Double-click
# "Installer Bibli (serveur).cmd"; this script elevates itself. Re-running it
# updates the program in place and keeps the database. -Unattended takes the
# password from BIBLI_ADMIN_PASSWORD and asks nothing (CI, scripted installs).
param([switch]$Unattended)
$ErrorActionPreference = 'Stop'
# A volunteer must see what went wrong before the window closes.
trap { Write-Host "$_" -ForegroundColor Red; if (-not $Unattended) { [void](Read-Host) }; exit 1 }

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  if ($Unattended) { throw 'install.ps1 -Unattended must run as administrator' }
  Start-Process powershell.exe -Verb RunAs -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"")
  exit
}

Add-Type -AssemblyName System.Windows.Forms, System.Drawing
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

$src     = $PSScriptRoot
$prog    = Join-Path $env:ProgramFiles 'Bibli'
$data    = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'
$port    = 8080
$ico     = Join-Path $prog 'bibli.ico'

function Read-Password {
  $form = New-Object System.Windows.Forms.Form -Property @{
    Text = 'Bibli'; Width = 460; Height = 200; StartPosition = 'CenterScreen'
    FormBorderStyle = 'FixedDialog'; MaximizeBox = $false; MinimizeBox = $false; TopMost = $true }
  $label = New-Object System.Windows.Forms.Label -Property @{
    Text = $T.pw_prompt; Left = 12; Top = 12; Width = 420; Height = 54 }
  $box = New-Object System.Windows.Forms.TextBox -Property @{ Left = 12; Top = 72; Width = 420; UseSystemPasswordChar = $true }
  $ok = New-Object System.Windows.Forms.Button -Property @{ Text = 'OK'; Left = 332; Top = 112; Width = 100; DialogResult = 'OK' }
  $form.Controls.AddRange(@($label, $box, $ok)); $form.AcceptButton = $ok
  if ($form.ShowDialog() -eq 'OK') { return $box.Text }
  return $null
}
function Find-Browser {
  @("${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
    "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
    "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe") |
    Where-Object { Test-Path $_ } | Select-Object -First 1
}
function New-Shortcut($Path, $Target, $Arguments, $WorkDir) {
  $shell = New-Object -ComObject WScript.Shell
  $lnk = $shell.CreateShortcut($Path)
  $lnk.TargetPath = $Target
  if ($Arguments) { $lnk.Arguments = $Arguments }
  if ($WorkDir)   { $lnk.WorkingDirectory = $WorkDir }
  if (Test-Path $ico) { $lnk.IconLocation = $ico }
  $lnk.Save()
}
# Well-known SIDs, not names: account names are localised (French Windows says
# "Administrateurs", "SERVICE LOCAL"), and icacls would not find them.
$sidSystem = '*S-1-5-18'; $sidAdmins = '*S-1-5-32-544'; $sidLocalService = '*S-1-5-19'
function Invoke-Icacls { icacls @args | Out-Null; if ($LASTEXITCODE -ne 0) { throw "icacls $args failed ($LASTEXITCODE)" } }
function Invoke-Sc { sc.exe @args | Out-Null; if ($LASTEXITCODE -ne 0) { throw "sc.exe $args failed ($LASTEXITCODE)" } }

Write-Host ($T.installing -f $prog, $data)

# 0. On a re-run, stop the server first: Windows locks a running bibli.exe.
$existing = Get-Service -Name $service -ErrorAction SilentlyContinue
if ($existing) {
  Stop-Service -Name $service -Force
  $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}

# 1. Program files, replaced on every run. The database stays in ProgramData.
New-Item -ItemType Directory -Force -Path $prog | Out-Null
foreach ($f in 'bibli.exe','bibli.ico','bibli-tray.ico','lang.ps1','bibli-admin.ps1','update.ps1','uninstall.ps1','client-shortcut.ps1','tray-icon.ps1') {
  Copy-Item -Force (Join-Path $src $f) (Join-Path $prog $f)
}
Get-ChildItem $prog | Unblock-File -ErrorAction SilentlyContinue

# 2. Data directory, writable by LOCAL SERVICE, the account the server runs as.
New-Item -ItemType Directory -Force -Path $data, (Join-Path $data 'logs'), (Join-Path $data 'backups') | Out-Null
Invoke-Icacls $data /grant "${sidLocalService}:(OI)(CI)M" /T /Q

# 3. Librarian password, asked once, stored where only the server, SYSTEM and
#    administrators can read it. A network instance needs 12+ characters.
$pwFile = Join-Path $data 'password'
if (-not (Test-Path $pwFile)) {
  if ($Unattended) {
    $pw = $env:BIBLI_ADMIN_PASSWORD
    if (-not $pw -or $pw.Length -lt 12) { throw 'BIBLI_ADMIN_PASSWORD must hold at least 12 characters' }
  } else {
    do {
      $pw = Read-Password
      if ($null -eq $pw) { Write-Host $T.cancelled; exit 1 }
      $ok = $pw.Length -ge 12
      if (-not $ok) { [System.Windows.Forms.MessageBox]::Show($T.pw_tooshort, 'Bibli', 'OK', 'Warning') | Out-Null }
    } while (-not $ok)
  }
  [IO.File]::WriteAllText($pwFile, $pw)
}
Invoke-Icacls $pwFile /inheritance:r /grant:r "${sidSystem}:F" "${sidAdmins}:F" "${sidLocalService}:R"

# 4. The service: starts with the PC as LOCAL SERVICE (a network-facing server
#    needs no more rights), restarted by Windows after a crash. The service
#    manager knows "NT AUTHORITY\LocalService" in every language.
$bin = ('"{0}" -db "{1}" -addr :{2} -secure-cookies=false -family-links=false ' +
        '-backup-dir "{3}" -cache-dir "{4}" -log-file "{5}" -password-file "{6}"') -f
  (Join-Path $prog 'bibli.exe'), (Join-Path $data 'biblio.db'), $port,
  (Join-Path $data 'backups'), (Join-Path $data 'cache'), (Join-Path $data 'logs\bibli.log'), $pwFile
if (-not $existing) {
  New-Service -Name $service -BinaryPathName $bin -DisplayName 'Bibli' -Description $T.svc_desc -StartupType Automatic | Out-Null
}
# Through CIM, not "sc.exe config": Windows PowerShell 5 mangles the quotes
# inside a native command's argument.
$r = Get-CimInstance Win32_Service -Filter "Name='$service'" |
  Invoke-CimMethod -MethodName Change -Arguments @{ PathName = $bin; StartName = 'NT AUTHORITY\LocalService'; StartPassword = '' }
if ($r.ReturnValue -ne 0) { throw "service configuration failed (Win32_Service.Change returned $($r.ReturnValue))" }
Invoke-Sc failure $service reset= 86400 actions= restart/5000/restart/10000/restart/60000
Invoke-Sc failureflag $service 1

# 4b. A server must not sleep: on mains power, never go to sleep or hibernate.
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0

# 5. Firewall: port 8080 inbound, private and domain networks only.
if (-not (Get-NetFirewallRule -DisplayName 'Bibli' -ErrorAction SilentlyContinue)) {
  New-NetFirewallRule -DisplayName 'Bibli' -Direction Inbound -Action Allow `
    -Protocol TCP -LocalPort $port -Profile Domain,Private | Out-Null
}

# 6. Shortcuts: a "Bibli" icon for daily use, and the admin menu.
$url      = "http://$($env:COMPUTERNAME):$port/"
$desktop  = [Environment]::GetFolderPath('CommonDesktopDirectory')
$programs = [Environment]::GetFolderPath('CommonPrograms')
$browser  = Find-Browser
foreach ($folder in @($desktop, $programs)) {
  $lnk = Join-Path $folder 'Bibli.lnk'
  if ($browser) {
    New-Shortcut $lnk $browser "--app=$url --no-first-run --no-default-browser-check" $prog
  } else {
    "[InternetShortcut]`r`nURL=$url`r`nIconFile=$ico`r`nIconIndex=0" |
      Set-Content -Encoding ASCII (Join-Path $folder 'Bibli.url')
  }
}

$menu = Join-Path $programs $T.menu_folder
New-Item -ItemType Directory -Force -Path $menu | Out-Null
$ps    = (Get-Command powershell.exe).Source
$admin = Join-Path $prog 'bibli-admin.ps1'
function New-AdminShortcut($name, $actionName) {
  New-Shortcut (Join-Path $menu "$name.lnk") $ps `
    ("-NoProfile -ExecutionPolicy Bypass -File `"$admin`" -Action $actionName") $prog
}
New-AdminShortcut $T.sc_open      'open'
New-AdminShortcut $T.sc_address   'address'
New-AdminShortcut $T.sc_status    'status'
New-AdminShortcut $T.sc_start     'start'
New-AdminShortcut $T.sc_stop      'stop'
New-AdminShortcut $T.sc_restart   'restart'
New-AdminShortcut $T.sc_logs      'logs'
New-AdminShortcut $T.sc_data      'data'
New-AdminShortcut $T.sc_update    'update'
New-AdminShortcut $T.sc_uninstall 'uninstall'

# 7. Tray icon: a shortcut in the common Startup folder launches it at each
#    login, and we start it now so it appears straight away.
$trayArgs = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$(Join-Path $prog 'tray-icon.ps1')`""
New-Shortcut (Join-Path ([Environment]::GetFolderPath('CommonStartup')) 'Bibli (icone).lnk') $ps $trayArgs $prog
if (-not $Unattended) { Start-Process $ps -WindowStyle Hidden -ArgumentList $trayArgs }

# 8. Start the server now and wait for the first answer.
Start-Service -Name $service
$up = $false
for ($i = 0; $i -lt 50 -and -not $up; $i++) {
  try { $up = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 1 "http://localhost:$port/healthcheck").StatusCode -eq 200 }
  catch { Start-Sleep -Milliseconds 300 }
}

Write-Host ''
if ($up) {
  Write-Host $T.sum_running
  Write-Host ($T.sum_thispc -f "http://localhost:$port/")
  Write-Host ($T.sum_others -f $url)
  Write-Host $T.sum_icons1
  Write-Host $T.sum_icons2
} else {
  Write-Host ($T.sum_notup -f (Join-Path $data 'logs\bibli.log'))
  if ($Unattended) { exit 1 }
}
Write-Host ''
if (-not $Unattended) { Read-Host $T.done_enter }
PS1

# --- bibli-admin.ps1: the small actions behind the admin menu ---
cat > "$DIR/bibli-admin.ps1" <<'PS1'
# Backs the admin Start-menu shortcuts. One action per shortcut.
param([Parameter(Mandatory)][string]$Action)
$ErrorActionPreference = 'SilentlyContinue'
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings
$prog = Join-Path $env:ProgramFiles 'Bibli'
$data = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'
$port = 8080
$url  = "http://$($env:COMPUTERNAME):$port/"

function Need-Admin {
  if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell.exe -Verb RunAs -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"",'-Action',$Action)
    exit
  }
}
function Pause-Key { Write-Host ''; Write-Host $T.press_enter; [void](Read-Host) }
function Invoke-Service($block, $done) {
  try { & $block; Write-Host $done } catch { Write-Host "$_" -ForegroundColor Red }
  Pause-Key
}

switch ($Action) {
  'open'    { Start-Process $url }
  'start'   { Need-Admin; Invoke-Service { Start-Service -Name $service -ErrorAction Stop } $T.msg_started }
  'stop'    { Need-Admin; Invoke-Service { Stop-Service -Name $service -ErrorAction Stop } $T.msg_stopped }
  'restart' { Need-Admin; Invoke-Service { Restart-Service -Name $service -ErrorAction Stop } $T.msg_restarted }
  'status'  {
      $up = $false
      try { $up = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://localhost:$port/healthcheck").StatusCode -eq 200 } catch {}
      Write-Host "$($T.lbl_task) $((Get-Service -Name $service).Status)"
      Write-Host "$($T.lbl_responds) $(if ($up) { $T.yes } else { $T.no })"
      Write-Host "$($T.lbl_address) $url"
      Pause-Key
  }
  'address' {
      Write-Host $T.addr_intro
      Write-Host "  $url"
      Get-NetIPAddress -AddressFamily IPv4 |
        Where-Object { $_.IPAddress -ne '127.0.0.1' -and $_.IPAddress -notlike '169.254.*' } |
        ForEach-Object { Write-Host ("  http://{0}:{1}/" -f $_.IPAddress, $port) }
      Pause-Key
  }
  'logs'    { Start-Process notepad.exe (Join-Path $data 'logs\bibli.log') }
  'data'    { Start-Process explorer.exe $data }
  'update'    { & (Join-Path $prog 'update.ps1') }
  'uninstall' { & (Join-Path $prog 'uninstall.ps1') }
  default   { Write-Host "Unknown action: $Action" }
}
PS1

# --- tray-icon.ps1: a per-user system-tray icon, started at each login ---
cat > "$DIR/tray-icon.ps1" <<'PS1'
# A per-user system-tray icon for the Bibli server, started at each login by a
# shortcut in the common Startup folder. It does NOT run the server itself —
# that is the "Bibli" Windows service, under LOCAL SERVICE. The tray only opens
# Bibli and starts/stops/restarts the service; those three need administrator rights, so
# Windows asks for confirmation (UAC) each time. The hide item removes the icon
# only; the server keeps running.
$ErrorActionPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

$prog = Join-Path $env:ProgramFiles 'Bibli'
# A tray-tuned glyph (brighter, full bleed, no panel) that reads at 16 px on any
# taskbar; fall back to the full logo if it is missing.
$ico  = Join-Path $prog 'bibli-tray.ico'
if (-not (Test-Path $ico)) { $ico = Join-Path $prog 'bibli.ico' }
$service = 'Bibli'
$port = 8080
$url  = "http://$($env:COMPUTERNAME):$port/"

function Test-Up {
  try { return (Invoke-WebRequest -UseBasicParsing -TimeoutSec 1 "http://localhost:$port/healthcheck").StatusCode -eq 200 }
  catch { return $false }
}
function Open-Bibli {
  $b = @("${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
         "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
         "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
         "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe") |
    Where-Object { Test-Path $_ } | Select-Object -First 1
  if ($b) { Start-Process $b -ArgumentList "--app=$url", '--no-first-run', '--no-default-browser-check' }
  else    { Start-Process $url }
}
# The balloon only follows a command that ran: a cancelled UAC prompt throws.
function Invoke-Admin($command, $balloon) {
  try {
    $p = Start-Process powershell.exe -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ErrorAction Stop `
      -ArgumentList '-NoProfile', '-Command', $command
  } catch { return }
  if ($p.ExitCode -ne 0) { return }
  $script:notify.BalloonTipTitle = 'Bibli'; $script:notify.BalloonTipText = $balloon; $script:notify.ShowBalloonTip(3000)
}

$script:notify = New-Object System.Windows.Forms.NotifyIcon
if (Test-Path $ico) { $script:notify.Icon = New-Object System.Drawing.Icon($ico) }
else { $script:notify.Icon = [System.Drawing.SystemIcons]::Application }
$script:notify.Text = 'Bibli'
$script:notify.Visible = $true

$menu = New-Object System.Windows.Forms.ContextMenuStrip
function Add-Item($text, $action) { $menu.Items.Add($text).Add_Click($action) }
function Add-Sep { $menu.Items.Add((New-Object System.Windows.Forms.ToolStripSeparator)) | Out-Null }

Add-Item $T.sc_open { Open-Bibli }
Add-Item "$($T.sc_address)…" {
  $ips = (Get-NetIPAddress -AddressFamily IPv4 |
    Where-Object { $_.IPAddress -ne '127.0.0.1' -and $_.IPAddress -notlike '169.254.*' } |
    ForEach-Object { "http://$($_.IPAddress):$port/" }) -join "`n"
  [System.Windows.Forms.MessageBox]::Show("$($T.addr_intro)`n`n$url`n$ips", 'Bibli') | Out-Null
}
Add-Item "$($T.sc_status)…" {
  $state = (Get-Service -Name $service).Status
  $up = if (Test-Up) { $T.yes } else { $T.no }
  [System.Windows.Forms.MessageBox]::Show("$($T.lbl_task) $state`n$($T.lbl_responds) $up`n$($T.lbl_address) $url", 'Bibli') | Out-Null
}
Add-Sep
Add-Item $T.sc_start   { Invoke-Admin "Start-Service -Name $service" $T.msg_started }
Add-Item $T.sc_stop    { Invoke-Admin "Stop-Service -Name $service" $T.msg_stopped }
Add-Item $T.sc_restart { Invoke-Admin "Restart-Service -Name $service" $T.msg_restarted }
Add-Sep
Add-Item $T.t_hide {
  $script:notify.Visible = $false; $script:timer.Stop(); [System.Windows.Forms.Application]::Exit()
}
$script:notify.ContextMenuStrip = $menu
$script:notify.Add_MouseDoubleClick({ Open-Bibli })

$script:timer = New-Object System.Windows.Forms.Timer
$script:timer.Interval = 20000
$updateTip = { $script:notify.Text = if (Test-Up) { $T.tip_up } else { $T.tip_down } }
$script:timer.Add_Tick($updateTip)
& $updateTip
$script:timer.Start()

[System.Windows.Forms.Application]::Run((New-Object System.Windows.Forms.ApplicationContext))
$script:notify.Dispose()
PS1

# --- update.ps1: swap bibli.exe for a newer one, keep the database ---
cat > "$DIR/update.ps1" <<'PS1'
# Updates Bibli without touching the database: stop the service, cold-copy the
# database, swap bibli.exe (and the scripts), start. Pick the downloaded
# Bibli-...-windows-server.zip, or a bare bibli.exe, when asked; -Package names
# it instead, and -Unattended skips the final pause.
param([string]$Package, [switch]$Unattended)
$ErrorActionPreference = 'Stop'
trap { Write-Host "$_" -ForegroundColor Red; if (-not $Unattended) { [void](Read-Host) }; exit 1 }
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  if ($Unattended) { throw 'update.ps1 -Unattended must run as administrator' }
  $a = @('-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"")
  if ($Package) { $a += @('-Package', "`"$Package`"") }
  Start-Process powershell.exe -Verb RunAs -ArgumentList $a
  exit
}
Add-Type -AssemblyName System.Windows.Forms
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

$prog    = Join-Path $env:ProgramFiles 'Bibli'
$data    = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'

$chosen = $Package
if (-not $chosen) {
  $dlg = New-Object System.Windows.Forms.OpenFileDialog
  $dlg.Title  = $T.up_title
  $dlg.Filter = "Bibli (bibli.exe;*.zip)|bibli.exe;*.zip|$($T.up_allfiles)|*.*"
  if ($dlg.ShowDialog() -ne 'OK') { exit }
  $chosen = $dlg.FileName
}

$tmp = $null
$folder = $null
if ($chosen -like '*.zip') {
  $tmp = Join-Path $env:TEMP ('bibli-update-' + [guid]::NewGuid())
  Expand-Archive -Path $chosen -DestinationPath $tmp -Force
  $exe = Get-ChildItem -Recurse -Path $tmp -Filter 'bibli.exe' | Select-Object -First 1
  if (-not $exe) { throw $T.up_err_zip }
  $folder = $exe.Directory.FullName
} else {
  $folder = Split-Path $chosen
}
if (-not (Test-Path (Join-Path $folder 'bibli.exe'))) { throw $T.up_err }

# The finally block starts the service again whatever fails, or the school
# stays offline. A stopped service is not restarted by Windows meanwhile.
try {
  Stop-Service -Name $service -Force
  (Get-Service -Name $service).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))

  # Cold copy of the (now closed) database, before the swap.
  $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
  $cold  = Join-Path $data "backups\pre-update-$stamp"
  New-Item -ItemType Directory -Force -Path $cold | Out-Null
  Get-ChildItem $data -Filter 'biblio.db*' -File -ErrorAction SilentlyContinue | Copy-Item -Destination $cold -Force

  # Refresh bibli.exe, and the scripts too when a full zip was given. Windows
  # may hold the old exe a moment after the process ends: retry the copy.
  for ($i = 1; ; $i++) {
    try { Copy-Item -Force (Join-Path $folder 'bibli.exe') (Join-Path $prog 'bibli.exe'); break }
    catch { if ($i -ge 10) { throw }; Start-Sleep 1 }
  }
  if ($chosen -like '*.zip') {
    Get-ChildItem $folder -File | Where-Object { $_.Extension -in '.ps1', '.ico' -and $_.Name -ne 'install.ps1' } |
      ForEach-Object { Copy-Item -Force $_.FullName (Join-Path $prog $_.Name) }
  }
  Get-ChildItem $prog | Unblock-File -ErrorAction SilentlyContinue
} finally {
  if ($tmp) { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
  Start-Service -Name $service
}
Write-Host ($T.up_done -f $cold)
Write-Host ''
if (-not $Unattended) { Read-Host $T.done_enter }
PS1

# --- uninstall.ps1: remove everything but keep the data unless -PurgeData ---
cat > "$DIR/uninstall.ps1" <<'PS1'
# Removes the Bibli server. The library data in ProgramData is KEPT (it holds
# children's records); pass -PurgeData to delete it too.
param([switch]$PurgeData, [switch]$Unattended)
$ErrorActionPreference = 'SilentlyContinue'
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  if ($Unattended) { Write-Host 'uninstall.ps1 -Unattended must run as administrator'; exit 1 }
  $a = @('-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"")
  if ($PurgeData) { $a += '-PurgeData' }
  Start-Process powershell.exe -Verb RunAs -ArgumentList $a
  exit
}
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

$prog = Join-Path $env:ProgramFiles 'Bibli'
$data = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'

Stop-Service -Name $service -Force
(Get-Service -Name $service).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
sc.exe delete $service | Out-Null

# Close any running tray icon (a hidden PowerShell running tray-icon.ps1).
Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" |
  Where-Object { $_.CommandLine -like '*tray-icon.ps1*' } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }

Remove-NetFirewallRule -DisplayName 'Bibli'

$desktop  = [Environment]::GetFolderPath('CommonDesktopDirectory')
$programs = [Environment]::GetFolderPath('CommonPrograms')
$startup  = [Environment]::GetFolderPath('CommonStartup')
Remove-Item -Force (Join-Path $desktop 'Bibli.lnk'), (Join-Path $desktop 'Bibli.url'), (Join-Path $programs 'Bibli.lnk'), (Join-Path $startup 'Bibli (icone).lnk')
# The admin menu folder, whatever language it was installed in.
foreach ($nm in 'Bibli (serveur)', 'Bibli (server)') { Remove-Item -Recurse -Force (Join-Path $programs $nm) }
Remove-Item -Recurse -Force $prog

if ($PurgeData) {
  Remove-Item -Recurse -Force $data
  Write-Host $T.un_all
} else {
  Write-Host ($T.un_kept -f $data)
}
Write-Host ''
if (-not $Unattended) { Read-Host $T.done_enter }
PS1

# --- client-shortcut.ps1: a "Bibli" icon on another PC or tablet ---
cat > "$DIR/client-shortcut.ps1" <<'PS1'
# Run this on another Windows PC or tablet to put a "Bibli" icon on its Desktop
# that opens the library on the school server. Pass -Server to name a different
# machine or IP (default: ask).
param([string]$Server = '', [int]$Port = 8080)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings
if (-not $Server) { $Server = Read-Host $T.cl_prompt }
if (-not $Server) { exit }
$url = "http://${Server}:$Port/"

# Keep a copy of the icon with the user so the shortcut can show it.
$dest = Join-Path $env:LOCALAPPDATA 'Bibli'
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Copy-Item -Force (Join-Path $PSScriptRoot 'bibli.ico') (Join-Path $dest 'bibli.ico')
$ico = Join-Path $dest 'bibli.ico'

$browser = @("${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
             "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
             "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
             "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe") |
  Where-Object { Test-Path $_ } | Select-Object -First 1

$desktop = [Environment]::GetFolderPath('Desktop')
if ($browser) {
  $shell = New-Object -ComObject WScript.Shell
  $lnk = $shell.CreateShortcut((Join-Path $desktop 'Bibli.lnk'))
  $lnk.TargetPath = $browser
  $lnk.Arguments  = "--app=$url --no-first-run --no-default-browser-check"
  $lnk.IconLocation = $ico
  $lnk.Save()
} else {
  "[InternetShortcut]`r`nURL=$url`r`nIconFile=$ico`r`nIconIndex=0" |
    Set-Content -Encoding ASCII (Join-Path $desktop 'Bibli.url')
}
Write-Host ($T.cl_done -f $url)
Read-Host $T.press_enter
PS1

# --- the double-click installer, the other-PC shortcut, a trilingual readme ---
# Double-clicking a .ps1 opens Notepad: each script a volunteer runs gets a .cmd.
cat > "$DIR/Installer Bibli (serveur).cmd" <<'CMD'
@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1"
CMD

cat > "$DIR/Raccourci Bibli (autre poste).cmd" <<'CMD'
@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0client-shortcut.ps1"
CMD

cat > "$DIR/LISEZ-MOI.txt" <<'TXT'
Bibli — serveur pour plusieurs postes (Windows)
-----------------------------------------------
Installer : double-cliquez sur « Installer Bibli (serveur).cmd », acceptez l'avertissement de Windows, puis choisissez le mot de passe de la bibliothèque. Bibli démarre alors tout seul à chaque allumage du PC, et une icône apparaît dans la zone de notification (près de l'horloge).
- Sur ce PC : l'icône « Bibli » sur le Bureau.
- Tablettes et autres postes : l'adresse donnée en fin d'installation (clic droit sur l'icône → « Adresse pour les tablettes »).
- Autre PC : copiez ce dossier dessus et double-cliquez sur « Raccourci Bibli (autre poste).cmd ».
Les scripts et l'icône parlent français, néerlandais ou anglais selon la langue de Windows. Détails : docs/deployment.md (« Windows (service) »).

Bibli — server voor meerdere computers (Windows)
------------------------------------------------
Installeren: dubbelklik op « Installer Bibli (serveur).cmd », aanvaard de Windows-waarschuwing en kies het wachtwoord van de bibliotheek. Bibli start voortaan bij elke start van de pc; een pictogram verschijnt in het systeemvak.
- Op deze pc: het pictogram « Bibli » op het bureaublad.
- Tablets en andere pc's: het adres dat na de installatie wordt getoond (rechtsklik op het pictogram → « Adres voor tablets »).
- Andere pc: kopieer deze map en dubbelklik op « Raccourci Bibli (autre poste).cmd ».

Bibli — server for several computers (Windows)
----------------------------------------------
Install: double-click "Installer Bibli (serveur).cmd", accept the Windows warning, then choose the library password. Bibli then starts on its own every time the PC powers on, and an icon appears in the notification area.
- On this PC: the "Bibli" icon on the Desktop.
- Tablets and other PCs: the address shown at the end of the install (right-click the icon -> "Address for tablets").
- Another PC: copy this folder onto it and double-click "Raccourci Bibli (autre poste).cmd".
TXT

# Windows PowerShell 5 reads a BOM-less script as the ANSI code page, so the
# accents would come out garbled. Prepend a UTF-8 BOM to every .ps1, then make
# every text file CRLF so Notepad shows them correctly.
for f in "$DIR"/*.ps1; do
    printf '\357\273\277' | cat - "$f" > "$WORK/x" && mv "$WORK/x" "$f"
done
for f in "$DIR"/*.ps1 "$DIR"/*.cmd "$DIR"/*.txt; do
    sed 's/$/\r/' "$f" > "$WORK/x" && mv "$WORK/x" "$f"
done

rm -f "$OUT/Bibli-windows-server.zip"
(cd "$WORK" && zip -qr "$OUT/Bibli-windows-server.zip" Bibli-serveur)
echo "Written: $OUT/Bibli-windows-server.zip"
