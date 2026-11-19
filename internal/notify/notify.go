// Package notify shows a macOS notification.
package notify

import (
	"os/exec"
	"strconv"
)

// Show posts a notification through Notification Center. Failures are
// ignored: a notification is a courtesy, never a requirement.
func Show(title, message string) {
	script := "display notification " + strconv.Quote(message) + " with title " + strconv.Quote(title)
	exec.Command("osascript", "-e", script).Run()
}
