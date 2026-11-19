package launchd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistIsValidAndRoundTrips(t *testing.T) {
	a := Agent{
		Program:    "/Users/x/Library/Application Support/claude-syncer/bin/claude-syncer",
		Args:       []string{"sync", "--background"},
		WatchPaths: []string{"/a & b/<c>", "/d"},
		Interval:   60,
		LogPath:    "/tmp/log",
	}
	data := a.Plist()
	if got := ProgramOf(data); got != a.Program {
		t.Errorf("ProgramOf = %q", got)
	}
	for _, want := range []string{"<key>AbandonProcessGroup</key>", "/a &amp; b/&lt;c&gt;", "<integer>60</integer>"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	// plutil is the authority on whether launchd will accept it.
	if _, err := exec.LookPath("plutil"); err == nil {
		path := filepath.Join(t.TempDir(), "agent.plist")
		os.WriteFile(path, data, 0o644)
		if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Errorf("plutil: %s", out)
		}
	}
}
