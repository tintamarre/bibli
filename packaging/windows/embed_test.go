package winsetup

import (
	"io/fs"
	"testing"
)

// bibli.exe must carry every script and icon the installer writes out.
func TestFiles(t *testing.T) {
	for _, name := range []string{
		"install.ps1", "uninstall.ps1", "app.ps1", "bibli-admin.ps1",
		"client-shortcut.ps1", "lang.ps1", "tray-icon.ps1", "bibli.ico", "bibli-tray.ico",
	} {
		if _, err := fs.Stat(Files, name); err != nil {
			t.Errorf("%s not embedded: %v", name, err)
		}
	}
}
