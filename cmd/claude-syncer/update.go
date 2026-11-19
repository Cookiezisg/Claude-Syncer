package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/notify"
)

const checkInterval = 24 * time.Hour

// updateInfo is cached in update.json so the check runs at most once a day.
type updateInfo struct {
	CheckedAt   time.Time `json:"checkedAt"`
	Latest      string    `json:"latest"`
	NotifiedFor string    `json:"notifiedFor,omitempty"`
}

func updatePath(p paths) string { return filepath.Join(p.appDir, "update.json") }

func updateCheckDisabled() bool {
	return version == "dev" || os.Getenv("CLAUDE_SYNCER_NO_UPDATE_CHECK") != ""
}

func loadUpdateInfo(p paths) updateInfo {
	var u updateInfo
	if data, err := os.ReadFile(updatePath(p)); err == nil {
		json.Unmarshal(data, &u)
	}
	return u
}

func saveUpdateInfo(p paths, u updateInfo) {
	if data, err := json.Marshal(u); err == nil {
		fsutil.WriteFileAtomic(updatePath(p), data, 0o600)
	}
}

// formulaURL is the published Homebrew formula. Its version is what
// `brew upgrade` installs; a GitHub release reaches the tap a few hours
// later, and announcing it before then would send people to an upgrade that
// does nothing yet.
const formulaURL = "https://raw.githubusercontent.com/Cookiezisg/homebrew-tap/main/Formula/claude-syncer.rb"

var formulaVersion = regexp.MustCompile(`/archive/refs/tags/v([0-9][0-9A-Za-z.\-]*)\.tar\.gz`)

// latestRelease reads the version of the published formula. The request
// carries nothing but the program's name and version.
func latestRelease() (string, error) {
	req, err := http.NewRequest("GET", formulaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "claude-syncer/"+version)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	return parseFormulaVersion(body), nil
}

func parseFormulaVersion(formula []byte) string {
	m := formulaVersion.FindSubmatch(formula)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// availableUpdate returns a newer released version, or "". It checks GitHub
// at most once a day and otherwise answers from the cache.
func availableUpdate(p paths) string {
	if updateCheckDisabled() {
		return ""
	}
	u := loadUpdateInfo(p)
	if time.Since(u.CheckedAt) > checkInterval {
		if latest, err := latestRelease(); err == nil {
			u.Latest = latest
		}
		u.CheckedAt = time.Now()
		saveUpdateInfo(p, u)
	}
	if newer(u.Latest, version) {
		return u.Latest
	}
	return ""
}

// notifyUpdate tells the user about a new release once per version.
func notifyUpdate(p paths) {
	latest := availableUpdate(p)
	if latest == "" {
		return
	}
	u := loadUpdateInfo(p)
	if u.NotifiedFor == latest {
		return
	}
	u.NotifiedFor = latest
	saveUpdateInfo(p, u)
	notify.Show("Claude Syncer "+latest+" is available", "Update with: brew upgrade claude-syncer")
}

// newer reports whether version a is newer than b (dotted numbers; a
// pre-release suffix sorts before the release).
func newer(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	pa, sa := splitVersion(a)
	pb, sb := splitVersion(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	// Same numbers: a release beats a pre-release.
	return sa == "" && sb != ""
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(v, "v")
	core, suffix, _ := strings.Cut(v, "-")
	var nums []int
	for _, part := range strings.Split(core, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, v
		}
		nums = append(nums, n)
	}
	return nums, suffix
}
