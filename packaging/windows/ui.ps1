# Shared WinForms helpers for the installer, the desktop app and the
# uninstaller: dialogs that lay themselves out one control under the other, so
# no translation is cut off at any display scaling; a password dialog that asks
# twice; the progress window. Dot-sourced next to lang.ps1; sizes are given at
# 96 dpi and scaled by Px.
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
# Declared before the first window exists, or Windows stretches (blurs) them.
if (-not ('Bibli.Dpi' -as [type])) {
  Add-Type -Namespace Bibli -Name Dpi -MemberDefinition '[System.Runtime.InteropServices.DllImport("user32.dll")] public static extern bool SetProcessDPIAware();'
}
[void][Bibli.Dpi]::SetProcessDPIAware()
[System.Windows.Forms.Application]::EnableVisualStyles()
$script:Scale = [System.Drawing.Graphics]::FromHwnd([IntPtr]::Zero).DpiX / 96
# The app's own colours (app/static/app.css).
$script:Accent = [System.Drawing.Color]::FromArgb(0x3a, 0x43, 0xa3)
$script:Muted  = [System.Drawing.Color]::FromArgb(0x4a, 0x51, 0x5d)
$script:Band   = [System.Drawing.Color]::FromArgb(0xee, 0xf0, 0xfa)

function Px([double]$n) { [int][Math]::Round($n * $script:Scale) }
function New-Pad([int]$Left, [int]$Top, [int]$Right, [int]$Bottom) {
  New-Object System.Windows.Forms.Padding -ArgumentList (Px $Left), (Px $Top), (Px $Right), (Px $Bottom)
}
function New-Font([single]$Size = 10, [System.Drawing.FontStyle]$Style = 'Regular') {
  New-Object System.Drawing.Font -ArgumentList 'Segoe UI', $Size, $Style
}

# A dialog $Width wide (content, at 96 dpi) that grows to fit what the Add-*
# helpers put in it. Add-Section starts a new full-width band; New-Dialog opens
# the first one.
function New-Dialog([int]$Width = 480) {
  $form = New-Object System.Windows.Forms.Form -Property @{
    Text = 'Bibli'; Font = (New-Font 10); AutoScaleMode = 'None'
    AutoSize = $true; AutoSizeMode = 'GrowAndShrink'; StartPosition = 'CenterScreen'
    FormBorderStyle = 'FixedDialog'; MaximizeBox = $false; MinimizeBox = $false; TopMost = $true
    BackColor = [System.Drawing.Color]::White }
  $icon = Join-Path $PSScriptRoot 'bibli.ico'
  if (Test-Path $icon) { $form.Icon = New-Object System.Drawing.Icon -ArgumentList $icon }
  $root = New-Object System.Windows.Forms.FlowLayoutPanel -Property @{
    FlowDirection = 'TopDown'; WrapContents = $false; AutoSize = $true; AutoSizeMode = 'GrowAndShrink'
    Margin = (New-Pad 0 0 0 0); Padding = (New-Pad 0 0 0 0) }
  $form.Controls.Add($root)
  $form.Tag = @{ Root = $root; Width = (Px $Width); Body = $null }
  [void](Add-Section $form)
  return $form
}
function Add-Section($form, $Color = [System.Drawing.Color]::White, [int]$PadY = 20) {
  $band = New-Object System.Windows.Forms.FlowLayoutPanel -Property @{
    FlowDirection = 'TopDown'; WrapContents = $false; AutoSize = $true; AutoSizeMode = 'GrowAndShrink'
    BackColor = $Color; Margin = (New-Pad 0 0 0 0); Padding = (New-Pad 24 $PadY 24 $PadY)
    MinimumSize = (New-Object System.Drawing.Size -ArgumentList ($form.Tag.Width + (Px 48)), 0) }
  $form.Tag.Root.Controls.Add($band)
  $form.Tag.Body = $band
  return $band
}
function Add-Label($form, [string]$Text, [single]$Size = 10, [switch]$Bold, $Color = $null, [int]$Gap = 10, [int]$Width = 0) {
  if (-not $Width) { $Width = $form.Tag.Width }
  $style = if ($Bold) { 'Bold' } else { 'Regular' }
  $label = New-Object System.Windows.Forms.Label -Property @{
    Text = $Text; AutoSize = $true; UseMnemonic = $false; Font = (New-Font $Size $style)
    MaximumSize = (New-Object System.Drawing.Size -ArgumentList $Width, 0); Margin = (New-Pad 0 0 0 $Gap) }
  if ($Color) { $label.ForeColor = $Color }
  $form.Tag.Body.Controls.Add($label)
  return $label
}
function Add-Heading($form, [string]$Text) { Add-Label $form $Text 13 -Bold -Color $script:Accent -Gap 12 }
function Add-TextBox($form, [switch]$Password) {
  $box = New-Object System.Windows.Forms.TextBox -Property @{
    Width = $form.Tag.Width; UseSystemPasswordChar = [bool]$Password; Margin = (New-Pad 0 0 0 12) }
  $form.Tag.Body.Controls.Add($box)
  return $box
}
# $Target is a URL to open, or a script block to run. Explorer opens the URL,
# so the browser runs as the signed-in user even from an elevated installer.
function New-Link([string]$Text, $Target) {
  $link = New-Object System.Windows.Forms.LinkLabel -Property @{
    Text = $Text; AutoSize = $true; Tag = $Target; LinkColor = $script:Accent; Margin = (New-Pad 0 0 16 4) }
  $link.Add_LinkClicked({
    if ($this.Tag -is [scriptblock]) { & $this.Tag } else { Start-Process explorer.exe "`"$($this.Tag)`"" }
  })
  return $link
}
# A row of links (or any controls) that wraps when the window is too narrow.
function Add-Row($form, [array]$Controls, [int]$Gap = 10) {
  $row = New-Object System.Windows.Forms.FlowLayoutPanel -Property @{
    FlowDirection = 'LeftToRight'; WrapContents = $true; AutoSize = $true; AutoSizeMode = 'GrowAndShrink'
    MaximumSize = (New-Object System.Drawing.Size -ArgumentList $form.Tag.Width, 0); Margin = (New-Pad 0 0 0 $Gap) }
  $row.Controls.AddRange($Controls)
  $form.Tag.Body.Controls.Add($row)
  return $row
}
# The buttons at the foot of a dialog, right-aligned, the first one leftmost
# (Windows puts OK before Cancel). Each item is @(text, DialogResult or $null).
function Add-Buttons($form, [array]$Items) {
  $row = New-Object System.Windows.Forms.FlowLayoutPanel -Property @{
    FlowDirection = 'RightToLeft'; WrapContents = $false; AutoSize = $true; AutoSizeMode = 'GrowAndShrink'
    MinimumSize = (New-Object System.Drawing.Size -ArgumentList $form.Tag.Width, 0); Margin = (New-Pad 0 8 0 0) }
  $buttons = @(foreach ($item in $Items) {
    $b = New-Object System.Windows.Forms.Button -Property @{
      Text = $item[0]; AutoSize = $true; FlatStyle = 'System'; Margin = (New-Pad 8 0 0 0)
      Padding = (New-Pad 10 0 10 0); MinimumSize = (New-Object System.Drawing.Size -ArgumentList (Px 104), (Px 34)) }
    if ($item[1]) { $b.DialogResult = $item[1] }
    $b
  })
  [array]::Reverse($buttons)
  $row.Controls.AddRange($buttons)
  [array]::Reverse($buttons)
  $form.Tag.Body.Controls.Add($row)
  return $buttons
}
function Show-Message([string]$Text, [string]$Icon = 'Information') {
  [void][System.Windows.Forms.MessageBox]::Show($Text, 'Bibli', 'OK', $Icon)
}

# The largest image in bibli.ico, which holds PNGs: Icon.ToBitmap would pick a
# small one and blur it. $null if it cannot be read.
function Get-Logo {
  $path = Join-Path $PSScriptRoot 'bibli.ico'
  if (-not (Test-Path $path)) { return $null }
  try {
    $b = [IO.File]::ReadAllBytes($path)
    $best = $null
    for ($i = 0; $i -lt [BitConverter]::ToUInt16($b, 4); $i++) {
      $e = 6 + 16 * $i
      $w = if ($b[$e] -eq 0) { 256 } else { [int]$b[$e] }
      if (-not $best -or $w -gt $best.W) {
        $best = @{ W = $w; Size = [BitConverter]::ToInt32($b, $e + 8); Offset = [BitConverter]::ToInt32($b, $e + 12) }
      }
    }
    return [System.Drawing.Image]::FromStream((New-Object IO.MemoryStream -ArgumentList $b, $best.Offset, $best.Size))
  } catch { return $null }
}

# The command links of Windows' own dialogs: a bold title with a note under it,
# as tall as both need. Compiled on first use, a second the desktop app need
# not pay at each launch.
function New-CommandLink([string]$Title, [string]$Note, [int]$Width) {
  if (-not ('Bibli.CommandLink' -as [type])) {
    Add-Type -ReferencedAssemblies System.Windows.Forms, System.Drawing -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Windows.Forms;
namespace Bibli {
  public class CommandLink : Button {
    [StructLayout(LayoutKind.Sequential)] struct SIZE { public int cx, cy; }
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern IntPtr SendMessage(IntPtr h, int msg, IntPtr w, string l);
    [DllImport("user32.dll")] static extern IntPtr SendMessage(IntPtr h, int msg, IntPtr w, ref SIZE l);
    const int BS_COMMANDLINK = 0x0E, BCM_GETIDEALSIZE = 0x1601, BCM_SETNOTE = 0x1609;
    string note = "";
    public CommandLink() { FlatStyle = FlatStyle.System; }
    protected override CreateParams CreateParams {
      get { CreateParams p = base.CreateParams; p.Style |= BS_COMMANDLINK; return p; }
    }
    public string Note { get { return note; } set { note = value ?? ""; if (IsHandleCreated) Fit(); } }
    protected override void OnHandleCreated(EventArgs e) { base.OnHandleCreated(e); Fit(); }
    void Fit() {
      SendMessage(Handle, BCM_SETNOTE, IntPtr.Zero, note);
      SIZE s = new SIZE(); s.cx = Width;
      SendMessage(Handle, BCM_GETIDEALSIZE, IntPtr.Zero, ref s);
      if (s.cy > Height) Height = s.cy;
    }
  }
}
'@
  }
  New-Object Bibli.CommandLink -Property @{
    Text = $Title; Note = $Note; Width = $Width; Height = (Px 64); Margin = (New-Pad 0 0 0 6) }
}

# The librarian password, typed twice: it is hidden, and a typo would lock the
# volunteer out of their own library. $null when cancelled.
function Read-NewPassword($T, [string]$Prompt, [int]$Min = 1) {
  $form = New-Dialog 460
  [void](Add-Heading $form $T.pw_heading)
  [void](Add-Label $form $Prompt -Gap 16)
  [void](Add-Label $form $T.pw_label -Gap 4)
  $box1 = Add-TextBox $form -Password
  [void](Add-Label $form $T.pw_confirm -Gap 4)
  $box2 = Add-TextBox $form -Password
  $show = New-Object System.Windows.Forms.CheckBox -Property @{ Text = $T.pw_show; AutoSize = $true; Margin = (New-Pad 0 0 0 8) }
  $form.Tag.Body.Controls.Add($show)
  $err = Add-Label $form '' -Color ([System.Drawing.Color]::Firebrick)
  $ok, $cancel = Add-Buttons $form @(@($T.ok, $null), @($T.cancel, 'Cancel'))
  $form.AcceptButton = $ok; $form.CancelButton = $cancel
  $show.Add_CheckedChanged({
    $box1.UseSystemPasswordChar = -not $show.Checked
    $box2.UseSystemPasswordChar = -not $show.Checked
  }.GetNewClosure())
  $ok.Add_Click({
    if ($box1.Text.Length -lt $Min) {
      $err.Text = if ($Min -le 1) { $T.pw_empty } else { $T.pw_tooshort -f $Min }
      $box1.Focus()
    } elseif ($box1.Text -cne $box2.Text) {
      $err.Text = $T.pw_mismatch
      $box2.Focus(); $box2.SelectAll()
    } else {
      $form.DialogResult = 'OK'
    }
  }.GetNewClosure())
  $result = if ($form.ShowDialog() -eq 'OK') { $box1.Text } else { $null }
  $form.Dispose()
  return $result
}

# One line of text; $null when cancelled.
function Read-Text($T, [string]$Prompt) {
  $form = New-Dialog 460
  [void](Add-Label $form $Prompt)
  $box = Add-TextBox $form
  $ok, $cancel = Add-Buttons $form @(@($T.ok, 'OK'), @($T.cancel, 'Cancel'))
  $form.AcceptButton = $ok; $form.CancelButton = $cancel
  $result = if ($form.ShowDialog() -eq 'OK') { $box.Text.Trim() } else { $null }
  $form.Dispose()
  return $result
}

# A window that names each step while the installer works, with no close
# button: the install must not be abandoned halfway. Without Start-Progress
# (unattended), Set-Step prints the step to the console instead.
function Start-Progress([int]$Steps, [string]$Heading) {
  $form = New-Dialog 460
  $form.ControlBox = $false
  [void](Add-Label $form $Heading -Bold)
  $status = Add-Label $form ' ' -Color $script:Muted
  $bar = New-Object System.Windows.Forms.ProgressBar -Property @{
    Width = $form.Tag.Width; Height = (Px 18); Minimum = 0; Maximum = $Steps; Margin = (New-Pad 0 4 0 0) }
  $form.Tag.Body.Controls.Add($bar)
  $form.Show()
  [System.Windows.Forms.Application]::DoEvents()
  $script:BibliProgress = @{ Form = $form; Status = $status; Bar = $bar; Done = 0 }
}
function Set-Step([string]$Text) {
  $p = $script:BibliProgress
  if (-not $p) { Write-Host $Text; return }
  $p.Status.Text = $Text
  $p.Bar.Value = [Math]::Min($p.Done, $p.Bar.Maximum)
  $p.Done++
  [System.Windows.Forms.Application]::DoEvents()
}
function Stop-Progress {
  $p = $script:BibliProgress
  if (-not $p) { return }
  $p.Bar.Value = $p.Bar.Maximum
  [System.Windows.Forms.Application]::DoEvents()
  $p.Form.Close(); $p.Form.Dispose()
  $script:BibliProgress = $null
}
