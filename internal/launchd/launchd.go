// Package launchd installs the background agent that runs a sync whenever
// session files change, and once a minute regardless.
package launchd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Label is the agent's launchd label.
const Label = "io.github.cookiezisg.claude-syncer"

// Agent describes the agent to install.
type Agent struct {
	Program    string   // absolute path of the claude-syncer binary
	Args       []string // arguments after the program
	WatchPaths []string
	Interval   int // seconds between runs when nothing changed
	LogPath    string
}

// PlistPath is where the agent definition lives.
func PlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// Plist renders the agent definition.
func (a Agent) Plist() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	key := func(k string) { fmt.Fprintf(&b, "\t<key>%s</key>\n", esc(k)) }
	str := func(indent, s string) { fmt.Fprintf(&b, "%s<string>%s</string>\n", indent, esc(s)) }
	array := func(k string, items []string) {
		key(k)
		b.WriteString("\t<array>\n")
		for _, it := range items {
			str("\t\t", it)
		}
		b.WriteString("\t</array>\n")
	}
	key("Label")
	str("\t", Label)
	array("ProgramArguments", append([]string{a.Program}, a.Args...))
	array("WatchPaths", a.WatchPaths)
	key("StartInterval")
	fmt.Fprintf(&b, "\t<integer>%d</integer>\n", a.Interval)
	key("RunAtLoad")
	b.WriteString("\t<true/>\n")
	// Let the detached helper that reloads this job outlive the pass that
	// started it.
	key("AbandonProcessGroup")
	b.WriteString("\t<true/>\n")
	// launchd will not start the job more often than this.
	key("ThrottleInterval")
	b.WriteString("\t<integer>5</integer>\n")
	// Not "Background": macOS throttles those so hard that a one-second
	// pass takes half a minute. Low-priority I/O and a nice value keep it
	// out of the way instead.
	key("ProcessType")
	str("\t", "Standard")
	key("LowPriorityIO")
	b.WriteString("\t<true/>\n")
	key("Nice")
	b.WriteString("\t<integer>10</integer>\n")
	key("StandardOutPath")
	str("\t", a.LogPath)
	key("StandardErrorPath")
	str("\t", a.LogPath)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Install writes the definition and (re)loads the agent.
func Install(a Agent) error {
	path := PlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.LogPath), 0o755); err != nil {
		return err
	}
	// Unload any previous version first; failure just means it was not
	// loaded.
	exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	if err := os.WriteFile(path, a.Plist(), 0o644); err != nil {
		return err
	}
	out, err := exec.Command("launchctl", "bootstrap", domain(), path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ReloadDetached writes the new definition and reloads the agent from a
// helper in its own session, for use from inside the agent: unloading the
// job stops its process, so the reload must not depend on this one
// surviving. If the reload fails, launchd still loads the new definition at
// the next login.
func ReloadDetached(a Agent) error {
	path := PlistPath()
	if err := os.WriteFile(path, a.Plist(), 0o644); err != nil {
		return err
	}
	target := domain() + "/" + Label
	script := fmt.Sprintf("sleep 2; launchctl bootout %s 2>/dev/null; launchctl bootstrap %s %s",
		shellQuote(target), shellQuote(domain()), shellQuote(path))
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var programRe = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)

// ProgramOf extracts the program path from a definition.
func ProgramOf(plist []byte) string {
	m := programRe.FindSubmatch(plist)
	if m == nil {
		return ""
	}
	return html.UnescapeString(string(m[1]))
}

// Uninstall unloads the agent and removes its definition.
func Uninstall() error {
	exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	err := os.Remove(PlistPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Loaded reports whether the agent is loaded.
func Loaded() bool {
	return exec.Command("launchctl", "print", domain()+"/"+Label).Run() == nil
}

// Installed reports whether the definition exists.
func Installed() bool { _, err := os.Stat(PlistPath()); return err == nil }
