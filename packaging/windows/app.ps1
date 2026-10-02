# The desktop app, for a single PC: starts Bibli on localhost, hidden, and
# opens it in a browser window of its own; closing that window stops the
# server. install.ps1 puts it in %LOCALAPPDATA%\Programs\Bibli with bibli.exe.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ui.ps1'); . (Join-Path $PSScriptRoot 'lang.ps1'); $T = Get-BibliStrings

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

$pwFile = Join-Path $data 'password'
if (-not (Test-Path $pwFile) -or -not (Get-Content -Raw $pwFile)) {
  $pw = Read-NewPassword $T $T.pw_prompt_app
  if (-not $pw) { exit }
  Set-Content -NoNewline -Encoding UTF8 -Path $pwFile -Value $pw
}

# A shortcut on the Desktop and in the Start menu, pointing at this folder:
# made on the first launch, and made again if the folder has moved.
$shell = New-Object -ComObject WScript.Shell
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  $lnk = $shell.CreateShortcut((Join-Path $folder 'Bibli.lnk'))
  if ($lnk.Arguments -notlike "*$here\app.ps1*") {
    $lnk.TargetPath = (Get-Command powershell.exe).Source
    $lnk.Arguments = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$here\app.ps1`""
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
  if (-not (Test-Running)) { Show-Message ($T.app_nostart -f "$data\bibli.log") 'Error'; exit 1 }
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
