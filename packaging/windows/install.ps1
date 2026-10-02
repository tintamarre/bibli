# Installs Bibli on Windows. bibli.exe runs it from a temporary folder when
# double-clicked, or as "bibli.exe install"; -Exe is that bibli.exe. On a PC
# without the service, it first asks what this PC is: the only one (the
# desktop app, for this user, no administrator needed), the server (a Windows
# service that starts with the PC), or another computer (a shortcut to the
# server). Re-running it updates the program in place and keeps the database.
# -Server skips the question; -Unattended also takes the password from
# BIBLI_ADMIN_PASSWORD and asks nothing (CI, scripted installs).
param([Parameter(Mandatory)][string]$Exe, [switch]$Server, [switch]$Unattended)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ui.ps1'); . (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings
# The console is usually hidden (a double-click), so a volunteer sees what went
# wrong in a message box, and the school is not left with its server stopped.
trap {
  Write-Host "$_" -ForegroundColor Red
  Stop-Progress
  Start-Service -Name 'Bibli' -ErrorAction SilentlyContinue
  if (-not $Unattended) { Show-Message "$_" 'Error' }
  exit 1
}

# One button per role, each answering with its own DialogResult.
function Read-Role {
  $form = New-Dialog 440 260
  [void](Add-Label $form $T.role_prompt 16 22 -Bold)
  $top = 48
  foreach ($b in @(@($T.role_app, 'OK'), @($T.role_server, 'Yes'), @($T.role_client, 'No'))) {
    $button = Add-Button $form $b[0] 16 $top 408 60
    $button.DialogResult = $b[1]; $button.TextAlign = 'MiddleLeft'
    $top += 68
  }
  $result = $form.ShowDialog()
  $form.Dispose()
  return $result
}
# Windows may hold an exe a moment after its process ends: retry the copy.
function Copy-Exe($dest) {
  if ((Test-Path $dest) -and (Resolve-Path $Exe).Path -eq (Resolve-Path $dest).Path) { return }
  for ($i = 1; ; $i++) {
    try { Copy-Item -Force $Exe $dest; return }
    catch { if ($i -ge 10) { throw }; Start-Sleep 1 }
  }
}
# The desktop app: program in the user's own Programs folder, data in
# %LOCALAPPDATA%\Bibli; app.ps1 asks the password and makes the shortcuts.
function Install-App {
  $dir = Join-Path $env:LOCALAPPDATA 'Programs\Bibli'
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  $dest = Join-Path $dir 'bibli.exe'
  Get-Process bibli -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $dest } | Stop-Process -Force
  Copy-Exe $dest
  foreach ($f in 'app.ps1', 'ui.ps1', 'lang.ps1', 'bibli.ico') { Copy-Item -Force (Join-Path $PSScriptRoot $f) (Join-Path $dir $f) }
  Get-ChildItem $dir | Unblock-File -ErrorAction SilentlyContinue
  Start-Process powershell.exe -WindowStyle Hidden -ArgumentList @(
    '-NoProfile','-ExecutionPolicy','Bypass','-WindowStyle','Hidden','-File',"`"$(Join-Path $dir 'app.ps1')`"")
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not ($Server -or $Unattended) -and -not (Get-Service -Name 'Bibli' -ErrorAction SilentlyContinue)) {
  switch (Read-Role) {
    'OK'  { Install-App; exit }
    'Yes' { }
    'No'  { & (Join-Path $PSScriptRoot 'client-shortcut.ps1'); exit }
    default { exit }
  }
}
if (-not $isAdmin) {
  if ($Unattended) { throw 'install.ps1 -Unattended must run as administrator' }
  # -Wait: bibli.exe deletes this folder as soon as this script returns.
  try {
    $p = Start-Process powershell.exe -Verb RunAs -Wait -PassThru -WindowStyle Hidden -ArgumentList @(
      '-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"",'-Exe',"`"$Exe`"",'-Server')
  } catch { exit 1 }  # the administrator prompt was declined
  exit $p.ExitCode
}

$src     = $PSScriptRoot
$prog    = Join-Path $env:ProgramFiles 'Bibli'
$data    = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'
$port    = 8080
$ico     = Join-Path $prog 'bibli.ico'

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

$pwFile = Join-Path $data 'password'

# The librarian password is asked first, once, so that cancelling leaves the PC
# untouched. A network instance needs 12+ characters.
$pw = $null
if (-not (Test-Path $pwFile)) {
  if ($Unattended) {
    $pw = $env:BIBLI_ADMIN_PASSWORD
    if (-not $pw -or $pw.Length -lt 12) { throw 'BIBLI_ADMIN_PASSWORD must hold at least 12 characters' }
  } else {
    $pw = Read-NewPassword $T $T.pw_prompt 12
    if ($null -eq $pw) { Write-Host $T.cancelled; exit 1 }
  }
}

if ($Unattended) { Write-Host ($T.installing -f $prog, $data) }
else { Start-Progress 7 ($T.installing -f $prog, $data) }

# 0. On a re-run, stop the server first (Windows locks a running bibli.exe),
#    and copy the closed database aside before the new version migrates it.
$existing = Get-Service -Name $service -ErrorAction SilentlyContinue
if ($existing) {
  Set-Step $T.st_stop
  Stop-Service -Name $service -Force
  $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
  if (Test-Path (Join-Path $data 'biblio.db')) {
    $cold = Join-Path $data ('backups\pre-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    New-Item -ItemType Directory -Force -Path $cold | Out-Null
    Get-ChildItem $data -Filter 'biblio.db*' -File | Copy-Item -Destination $cold -Force
    Write-Host ($T.up_saved -f $cold)
  }
}

# 1. Program files, replaced on every run. The database stays in ProgramData.
Set-Step $T.st_files
New-Item -ItemType Directory -Force -Path $prog | Out-Null
Copy-Exe (Join-Path $prog 'bibli.exe')
foreach ($f in 'bibli.ico','bibli-tray.ico','lang.ps1','ui.ps1','bibli-admin.ps1','uninstall.ps1','tray-icon.ps1') {
  Copy-Item -Force (Join-Path $src $f) (Join-Path $prog $f)
}
# Left by the older zip package, whose update the installer now does.
Remove-Item -Force (Join-Path $prog 'update.ps1') -ErrorAction SilentlyContinue
Get-ChildItem $prog | Unblock-File -ErrorAction SilentlyContinue

# 2. Data directory, writable by LOCAL SERVICE, the account the server runs as.
Set-Step $T.st_data
New-Item -ItemType Directory -Force -Path $data, (Join-Path $data 'logs'), (Join-Path $data 'backups') | Out-Null
Invoke-Icacls $data /grant "${sidLocalService}:(OI)(CI)M" /T /Q

# 3. Librarian password, stored where only the server, SYSTEM and administrators
#    can read it.
if ($pw) { [IO.File]::WriteAllText($pwFile, $pw) }
Invoke-Icacls $pwFile /inheritance:r /grant:r "${sidSystem}:F" "${sidAdmins}:F" "${sidLocalService}:R"

# 4. The service: starts with the PC as LOCAL SERVICE (a network-facing server
#    needs no more rights), restarted by Windows after a crash. The service
#    manager knows "NT AUTHORITY\LocalService" in every language.
Set-Step $T.st_service
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
Set-Step $T.st_network
if (-not (Get-NetFirewallRule -DisplayName 'Bibli' -ErrorAction SilentlyContinue)) {
  New-NetFirewallRule -DisplayName 'Bibli' -Direction Inbound -Action Allow `
    -Protocol TCP -LocalPort $port -Profile Domain,Private | Out-Null
}

# 6. Shortcuts: a "Bibli" icon for daily use, and the admin menu.
Set-Step $T.st_shortcuts
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
$startup = [Environment]::GetFolderPath('CommonStartup')
Remove-Item -Force (Join-Path $startup 'Bibli (icone).lnk') -ErrorAction SilentlyContinue  # its former name
New-Shortcut (Join-Path $startup 'Bibli.lnk') $ps $trayArgs $prog
if (-not $Unattended) { Start-Process $ps -WindowStyle Hidden -ArgumentList $trayArgs }

# 8. Start the server now and wait for the first answer.
Set-Step $T.st_start
Start-Service -Name $service
$up = $false
for ($i = 0; $i -lt 50 -and -not $up; $i++) {
  try { $up = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 1 "http://localhost:$port/healthcheck").StatusCode -eq 200 }
  catch { Start-Sleep -Milliseconds 300 }
}

Stop-Progress
if ($up) {
  $lines = @($T.sum_running, '', ($T.sum_thispc -f "http://localhost:$port/"), ($T.sum_others -f $url), '', $T.sum_icons1, $T.sum_icons2)
  $icon = 'Information'
} else {
  $lines = @($T.sum_notup -f (Join-Path $data 'logs\bibli.log'))
  $icon = 'Warning'
}
if ($Unattended) {
  $lines | ForEach-Object { Write-Host $_ }
  if (-not $up) { exit 1 }
} else {
  Show-Message ($lines -join "`n") $icon
}
