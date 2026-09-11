package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser asks the desktop to open url. It returns an error when no opener
// is available; callers print the URL regardless, so a failure here only
// costs the user a copy-paste.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening browser: %w", err)
	}
	return nil
}
