# Removes Bibli: the server (Windows service, firewall rule, tray icon,
# shortcuts, Start menu) or, with -App, this user's desktop app. Settings > Apps
# runs it, as do the server's menu and tray. The library data is KEPT (it holds
# readers' records) unless -PurgeData or the box in the dialog asks otherwise.
# -Confirmed skips the dialog (the elevated re-run); -Unattended asks nothing.
param([switch]$App, [switch]$PurgeData, [switch]$Confirmed, [switch]$Unattended)
$ErrorActionPreference = 'SilentlyContinue'
. (Join-Path $PSScriptRoot 'ui.ps1'); . (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

if ($App) {
  $prog = Join-Path $env:LOCALAPPDATA 'Programs\Bibli'
  $data = Join-Path $env:LOCALAPPDATA 'Bibli'
} else {
  $prog = Join-Path $env:ProgramFiles 'Bibli'
  $data = Join-Path $env:ProgramData 'Bibli'
}

# What goes, what stays, and a box to erase the data too, confirmed twice.
# $null when cancelled, otherwise whether to purge.
function Read-Uninstall {
  $form = New-Dialog 480
  [void](Add-Heading $form $T.un_heading)
  [void](Add-Label $form $(if ($App) { $T.un_what_app } else { $T.un_what_server }))
  [void](Add-Label $form ($T.un_keep -f $data) -Color $script:Muted -Gap 12)
  $purge = New-Object System.Windows.Forms.CheckBox -Property @{
    Text = $T.un_purge; AutoSize = $true; Margin = (New-Pad 0 0 0 8)
    MaximumSize = (New-Object System.Drawing.Size -ArgumentList $form.Tag.Width, 0) }
  $form.Tag.Body.Controls.Add($purge)
  $go, $cancel = Add-Buttons $form @(@($T.un_go, 'OK'), @($T.cancel, 'Cancel'))
  $form.CancelButton = $cancel
  $ok = $form.ShowDialog() -eq 'OK'
  $erase = $purge.Checked
  $form.Dispose()
  if (-not $ok) { return $null }
  if ($erase -and [System.Windows.Forms.MessageBox]::Show($T.un_purge_confirm, 'Bibli', 'YesNo', 'Warning', 'Button2') -ne 'Yes') { return $null }
  return $erase
}

# Asked first, as the volunteer, before any administrator prompt.
if (-not ($Confirmed -or $Unattended)) {
  $choice = Read-Uninstall
  if ($null -eq $choice) { exit }
  $PurgeData = [bool]$choice
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $App -and -not $isAdmin) {
  if ($Unattended) { Write-Host 'uninstall.ps1 -Unattended must run as administrator'; exit 1 }
  $a = @('-NoProfile','-ExecutionPolicy','Bypass','-WindowStyle','Hidden','-File',"`"$PSCommandPath`"",'-Confirmed')
  if ($PurgeData) { $a += '-PurgeData' }
  try { Start-Process powershell.exe -Verb RunAs -WindowStyle Hidden -ArgumentList $a -ErrorAction Stop }
  catch { Show-Message $T.un_needadmin 'Warning' }
  exit
}

$desktop  = [Environment]::GetFolderPath($(if ($App) { 'Desktop' } else { 'CommonDesktopDirectory' }))
$programs = [Environment]::GetFolderPath($(if ($App) { 'Programs' } else { 'CommonPrograms' }))
if ($App) {
  # Its server, and its browser window (the one using its own profile folder).
  Get-Process bibli | Where-Object { $_.Path -eq (Join-Path $prog 'bibli.exe') } | Stop-Process -Force
  Get-CimInstance Win32_Process -Filter "Name='msedge.exe' OR Name='chrome.exe'" |
    Where-Object { $_.CommandLine -like "*$data\browser*" } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
  Start-Sleep 1
  # Only the shortcuts that start this app: a shortcut to a server stays.
  $shell = New-Object -ComObject WScript.Shell
  foreach ($folder in $desktop, $programs) {
    $lnk = Join-Path $folder 'Bibli.lnk'
    if ((Test-Path $lnk) -and $shell.CreateShortcut($lnk).Arguments -like "*$prog\app.ps1*") { Remove-Item -Force $lnk }
  }
  Remove-Item -Recurse -Force (Join-Path $data 'browser')
  Remove-Item -Recurse -Force 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Bibli'
} else {
  $service = 'Bibli'
  Stop-Service -Name $service -Force
  (Get-Service -Name $service).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
  sc.exe delete $service | Out-Null

  # Close any running tray icon (a hidden PowerShell running tray-icon.ps1).
  Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" |
    Where-Object { $_.CommandLine -like '*tray-icon.ps1*' } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force }

  Remove-NetFirewallRule -DisplayName 'Bibli'

  $startup = [Environment]::GetFolderPath('CommonStartup')
  Remove-Item -Force (Join-Path $desktop 'Bibli.lnk'), (Join-Path $desktop 'Bibli.url'), (Join-Path $programs 'Bibli.lnk'), (Join-Path $programs 'Bibli.url'), (Join-Path $startup 'Bibli.lnk'), (Join-Path $startup 'Bibli (icone).lnk')
  # The admin menu folder, whatever language it was installed in.
  foreach ($nm in 'Bibli (serveur)', 'Bibli (server)') { Remove-Item -Recurse -Force (Join-Path $programs $nm) }
  Remove-Item -Force (Join-Path $env:APPDATA 'Bibli\tray-hidden')
  Remove-Item -Recurse -Force 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Bibli'
}
Remove-Item -Recurse -Force $prog

$done = if ($PurgeData) { Remove-Item -Recurse -Force $data; $T.un_all } else { $T.un_kept -f $data }
if ($Unattended) { Write-Host $done } else { Show-Message $done }
