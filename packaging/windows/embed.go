// Package winsetup holds the Windows installer and what it installs (the
// desktop app launcher, the server's tray icon and admin menu), embedded in
// bibli.exe.
package winsetup

import "embed"

//go:embed *.ps1 *.ico
var Files embed.FS
