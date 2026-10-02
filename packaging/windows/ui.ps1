# Shared WinForms helpers for the installer and the desktop app: dialogs that
# scale with the display, a password dialog that asks twice, and the progress
# window. Dot-sourced next to lang.ps1; coordinates are at 96 dpi.
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
# Declared before the first window exists, or Windows stretches (blurs) them.
if (-not ('Bibli.Dpi' -as [type])) {
  Add-Type -Namespace Bibli -Name Dpi -MemberDefinition '[System.Runtime.InteropServices.DllImport("user32.dll")] public static extern bool SetProcessDPIAware();'
}
[void][Bibli.Dpi]::SetProcessDPIAware()
[System.Windows.Forms.Application]::EnableVisualStyles()

function New-Dialog([int]$Width, [int]$Height) {
  $form = New-Object System.Windows.Forms.Form
  $form.Text = 'Bibli'
  $form.Font = [System.Drawing.SystemFonts]::MessageBoxFont
  $form.AutoScaleDimensions = New-Object System.Drawing.SizeF -ArgumentList 96, 96
  $form.AutoScaleMode = 'Dpi'
  $form.ClientSize = New-Object System.Drawing.Size -ArgumentList $Width, $Height
  $form.StartPosition = 'CenterScreen'
  $form.FormBorderStyle = 'FixedDialog'
  $form.MaximizeBox = $false; $form.MinimizeBox = $false; $form.TopMost = $true
  $icon = Join-Path $PSScriptRoot 'bibli.ico'
  if (Test-Path $icon) { $form.Icon = New-Object System.Drawing.Icon -ArgumentList $icon }
  return $form
}
function Add-Label($form, [string]$Text, [int]$Top, [int]$Height, [switch]$Bold) {
  $label = New-Object System.Windows.Forms.Label -Property @{
    Text = $Text; Left = 16; Top = $Top; Width = $form.ClientSize.Width - 32; Height = $Height }
  if ($Bold) { $label.Font = New-Object System.Drawing.Font -ArgumentList @($form.Font, [System.Drawing.FontStyle]::Bold) }
  $form.Controls.Add($label)
  return $label
}
function Add-Button($form, [string]$Text, [int]$Left, [int]$Top, [int]$Width, [int]$Height = 30) {
  $button = New-Object System.Windows.Forms.Button -Property @{
    Text = $Text; Left = $Left; Top = $Top; Width = $Width; Height = $Height; FlatStyle = 'System' }
  $form.Controls.Add($button)
  return $button
}
function Show-Message([string]$Text, [string]$Icon = 'Information') {
  [void][System.Windows.Forms.MessageBox]::Show($Text, 'Bibli', 'OK', $Icon)
}

# The librarian password, typed twice: it is hidden, and a typo would lock the
# volunteer out of their own library. $null when cancelled.
function Read-NewPassword($T, [string]$Prompt, [int]$Min = 1) {
  $form = New-Dialog 440 296
  [void](Add-Label $form $Prompt 16 56)
  [void](Add-Label $form $T.pw_label 80 18)
  $box1 = New-Object System.Windows.Forms.TextBox -Property @{ Left = 16; Top = 100; Width = 408; UseSystemPasswordChar = $true }
  [void](Add-Label $form $T.pw_confirm 134 18)
  $box2 = New-Object System.Windows.Forms.TextBox -Property @{ Left = 16; Top = 154; Width = 408; UseSystemPasswordChar = $true }
  $show = New-Object System.Windows.Forms.CheckBox -Property @{ Text = $T.pw_show; Left = 16; Top = 186; Width = 408; Height = 22 }
  $err = Add-Label $form '' 212 34
  $err.ForeColor = [System.Drawing.Color]::Firebrick
  $ok = Add-Button $form $T.ok 224 252 96
  $cancel = Add-Button $form $T.cancel 328 252 96
  $cancel.DialogResult = 'Cancel'
  $form.Controls.AddRange(@($box1, $box2, $show))
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
  $form = New-Dialog 440 150
  [void](Add-Label $form $Prompt 16 36)
  $box = New-Object System.Windows.Forms.TextBox -Property @{ Left = 16; Top = 56; Width = 408 }
  $ok = Add-Button $form $T.ok 224 100 96
  $ok.DialogResult = 'OK'
  $cancel = Add-Button $form $T.cancel 328 100 96
  $cancel.DialogResult = 'Cancel'
  $form.Controls.Add($box)
  $form.AcceptButton = $ok; $form.CancelButton = $cancel
  $result = if ($form.ShowDialog() -eq 'OK') { $box.Text.Trim() } else { $null }
  $form.Dispose()
  return $result
}

# A window that names each step while the installer works, with no close
# button: the install must not be abandoned halfway. Without Start-Progress
# (unattended), Set-Step prints the step to the console instead.
function Start-Progress([int]$Steps, [string]$Heading) {
  $form = New-Dialog 440 130
  $form.ControlBox = $false
  [void](Add-Label $form $Heading 16 36)
  $status = Add-Label $form '' 56 20
  $bar = New-Object System.Windows.Forms.ProgressBar -Property @{
    Left = 16; Top = 84; Width = 408; Height = 18; Minimum = 0; Maximum = $Steps }
  $form.Controls.Add($bar)
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
