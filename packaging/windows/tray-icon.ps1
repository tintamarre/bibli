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
