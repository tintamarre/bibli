# Puts a "Bibli" icon on the Desktop of another Windows PC or tablet, opening
# the library on the school server: what a double-click on bibli.exe does
# there, once told it is not the server. -Server names the machine or IP
# (default: ask).
param([string]$Server = '', [int]$Port = 8080)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ui.ps1'); . (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings
if (-not $Server) { $Server = Read-Text $T $T.cl_prompt }
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
Show-Message ($T.cl_done -f $url)
