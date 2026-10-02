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
Remove-Item -Force (Join-Path $desktop 'Bibli.lnk'), (Join-Path $desktop 'Bibli.url'), (Join-Path $programs 'Bibli.lnk'), (Join-Path $startup 'Bibli.lnk'), (Join-Path $startup 'Bibli (icone).lnk')
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
