# Backs the admin Start-menu shortcuts. One action per shortcut.
param([Parameter(Mandatory)][string]$Action)
$ErrorActionPreference = 'SilentlyContinue'
. (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings
$prog = Join-Path $env:ProgramFiles 'Bibli'
$data = Join-Path $env:ProgramData 'Bibli'
$service = 'Bibli'
$port = 8080
$url  = "http://$($env:COMPUTERNAME):$port/"
$local = "http://localhost:$port/"

function Need-Admin {
  if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell.exe -Verb RunAs -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"",'-Action',$Action)
    exit
  }
}
function Test-AutoStart { (Get-CimInstance Win32_Service -Filter "Name='$service'").StartMode -eq 'Auto' }
function Pause-Key { Write-Host ''; Write-Host $T.press_enter; [void](Read-Host) }
function Invoke-Service($block, $done) {
  try { & $block; Write-Host $done } catch { Write-Host "$_" -ForegroundColor Red }
  Pause-Key
}

switch ($Action) {
  'open'    { Start-Process $local }
  'start'   { Need-Admin; Invoke-Service { Start-Service -Name $service -ErrorAction Stop } $T.msg_started }
  'stop'    { Need-Admin; Invoke-Service { Stop-Service -Name $service -ErrorAction Stop } $T.msg_stopped }
  'restart' { Need-Admin; Invoke-Service { Restart-Service -Name $service -ErrorAction Stop } $T.msg_restarted }
  'status'  {
      $up = $false
      try { $up = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://localhost:$port/healthcheck").StatusCode -eq 200 } catch {}
      Write-Host "$($T.lbl_task) $((Get-Service -Name $service).Status)"
      Write-Host "$($T.lbl_responds) $(if ($up) { $T.yes } else { $T.no })"
      Write-Host "$($T.lbl_autostart) $(if (Test-AutoStart) { $T.yes } else { $T.no })"
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
  'autostart' {
      # Asked before the administrator prompt, which only applies the answer.
      Add-Type -AssemblyName System.Windows.Forms
      $on = Test-AutoStart
      $q = if ($on) { $T.as_off_q } else { $T.as_on_q }
      if ([System.Windows.Forms.MessageBox]::Show($q, 'Bibli', 'YesNo', 'Question') -eq 'Yes') {
        $mode = if ($on) { 'Manual' } else { 'Automatic' }
        try {
          $p = Start-Process powershell.exe -Verb RunAs -Wait -PassThru -WindowStyle Hidden -ErrorAction Stop `
            -ArgumentList '-NoProfile', '-Command', "Set-Service -Name $service -StartupType $mode"
          if ($p.ExitCode -eq 0) {
            [System.Windows.Forms.MessageBox]::Show($(if ($on) { $T.msg_auto_off } else { $T.msg_auto_on }), 'Bibli') | Out-Null
          }
        } catch {}  # the administrator prompt was declined
      }
  }
  'tray'    {
      Remove-Item -Force (Join-Path $env:APPDATA 'Bibli\tray-hidden')
      $running = Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" |
        Where-Object { $_.CommandLine -like '*tray-icon.ps1*' -and $_.ProcessId -ne $PID }
      if (-not $running) {
        Start-Process powershell.exe -WindowStyle Hidden -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-WindowStyle', 'Hidden', '-File', "`"$(Join-Path $prog 'tray-icon.ps1')`""
      }
  }
  'logs'    { Start-Process notepad.exe (Join-Path $data 'logs\bibli.log') }
  'data'    { Start-Process explorer.exe $data }
  'update'    {
      # The new bibli.exe installs itself over this one (install.ps1).
      Add-Type -AssemblyName System.Windows.Forms
      $dlg = New-Object System.Windows.Forms.OpenFileDialog
      $dlg.Title  = $T.up_title
      $dlg.Filter = 'Bibli (*.exe)|*.exe'
      if ($dlg.ShowDialog() -eq 'OK') { Start-Process $dlg.FileName -ArgumentList 'install' }
  }
  'uninstall' {
      Start-Process powershell.exe -WindowStyle Hidden -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-WindowStyle', 'Hidden', '-File', "`"$(Join-Path $prog 'uninstall.ps1')`""
  }
  default   { Write-Host "Unknown action: $Action" }
}
