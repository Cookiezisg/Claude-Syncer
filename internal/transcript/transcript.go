// Package transcript reads Claude Code's session transcripts
// (~/.claude/projects/<encoded cwd>/<session id>.jsonl).
//
// Transcripts are append-only JSON Lines files and can be hundreds of MB, so
// metadata is extracted incrementally: a Meta remembers the byte offset it
// has read up to, and the next scan continues from there.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
)

// File is one transcript on disk.
type File struct {
	ID      string // CLI session id
	Path    string
	Dir     string // project directory
	ModTime time.Time
	Size    int64
}

// ReleasedMarker is the file Claude Desktop leaves next to a transcript it
// removed because its session was deleted.
func ReleasedMarker(dir, id string) string { return filepath.Join(dir, id+".desktop-released.json") }

// List returns every top-level <uuid>.jsonl under projectsDir, plus the ids
// that carry a desktop-released marker.
func List(projectsDir string) (files []File, released map[string]bool, err error) {
	released = map[string]bool{}
	projects, err := os.ReadDir(projectsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, released, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if id, ok := strings.CutSuffix(name, ".desktop-released.json"); ok && desktop.IsUUID(id) {
				released[id] = true
				continue
			}
			id, ok := strings.CutSuffix(name, ".jsonl")
			if !ok || !desktop.IsUUID(id) || !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, File{ID: id, Path: filepath.Join(dir, name), Dir: dir, ModTime: info.ModTime(), Size: info.Size()})
		}
	}
	return files, released, nil
}

// Meta is what the sync needs from a transcript.
type Meta struct {
	Offset int64 `json:"o"` // bytes read so far (always at a line boundary)
	Size   int64 `json:"s"` // file size when last read
	MTime  int64 `json:"m"` // file mtime (unix ms) when last read

	Entrypoint     string `json:"e,omitempty"`
	CWD            string `json:"c,omitempty"`
	FirstTS        int64  `json:"f,omitempty"` // first entry timestamp, unix ms
	LastTS         int64  `json:"l,omitempty"` // last conversation entry timestamp, unix ms
	HasUserMessage bool   `json:"u,omitempty"`
	FirstUserText  string `json:"t,omitempty"`
	CustomTitle    string `json:"ct,omitempty"`
	AITitle        string `json:"at,omitempty"`
	Summary        string `json:"sm,omitempty"`
}

// Title picks the best title: a user-set title, then a generated one, then
// the first thing the user said.
func (m *Meta) Title() string {
	for _, t := range []string{m.CustomTitle, m.AITitle, m.Summary, m.FirstUserText} {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return ""
}

// IsSDK reports whether the transcript was written by an SDK or headless
// (`claude -p`) run rather than an interactive session.
func (m *Meta) IsSDK() bool { return strings.HasPrefix(m.Entrypoint, "sdk") }

// IsDesktop reports whether Claude Desktop wrote the transcript.
func (m *Meta) IsDesktop() bool { return m.Entrypoint == "claude-desktop" }

// Update brings m up to date with the file, reading only what was appended
// since the last call. A file that shrank or was replaced is read again from
// the start.
func Update(m *Meta, f File) error {
	mtime := f.ModTime.UnixMilli()
	if m.Size == f.Size && m.MTime == mtime && m.Offset > 0 {
		return nil
	}
	if f.Size < m.Offset {
		*m = Meta{}
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()
	if _, err := fh.Seek(m.Offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(fh, 256<<10)
	off := m.Offset
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			off += int64(len(line))
			m.consume(line)
		} else {
			// Partial last line: a writer is mid-append. Leave it for the
			// next scan.
			break
		}
		if err != nil {
			break
		}
	}
	m.Offset, m.Size, m.MTime = off, f.Size, mtime
	return nil
}

type entry struct {
	Type        string          `json:"type"`
	Entrypoint  string          `json:"entrypoint"`
	CWD         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     *message        `json:"message"`
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
	Summary     string          `json:"summary"`
	ToolUse     json.RawMessage `json:"toolUseResult"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m *Meta) consume(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "custom-title":
		m.CustomTitle = e.CustomTitle
		return
	case "ai-title":
		m.AITitle = e.AITitle
		return
	case "summary":
		m.Summary = e.Summary
		return
	}
	if m.Entrypoint == "" && e.Entrypoint != "" {
		m.Entrypoint = e.Entrypoint
	}
	if m.CWD == "" && e.CWD != "" {
		m.CWD = e.CWD
	}
	if e.Type != "user" && e.Type != "assistant" {
		return
	}
	if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		ms := ts.UnixMilli()
		if m.FirstTS == 0 || ms < m.FirstTS {
			m.FirstTS = ms
		}
		if ms > m.LastTS {
			m.LastTS = ms
		}
	}
	if e.Type != "user" || e.IsSidechain || e.IsMeta || e.Message == nil || e.Message.Role != "user" || len(e.ToolUse) > 0 {
		return
	}
	if text := userText(e.Message.Content); text != "" {
		m.HasUserMessage = true
		if m.FirstUserText == "" {
			m.FirstUserText = truncate(oneLine(titleText(text)), 120)
		}
	}
}

// Blocks pasted into a message, and other tagged blocks, make poor titles.
var taggedBlock = regexp.MustCompile(`(?s)<(pasted_content|system-reminder|command-[a-z-]+)[^>]*>.*?</(pasted_content|system-reminder|command-[a-z-]+)[^>]*>`)

func titleText(s string) string {
	if t := strings.TrimSpace(taggedBlock.ReplaceAllString(s, " ")); t != "" {
		return t
	}
	return s
}

// userText returns what the person typed, skipping tool results, slash
// command echoes and injected system text.
func userText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		if isTyped(s) {
			return s
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" && isTyped(b.Text) {
			return b.Text
		}
	}
	return ""
}

func isTyped(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, p := range []string{"<system-reminder>", "<command-", "<local-command-", "<bash-", "<user-prompt-submit-hook>", "Caveat: ", "[Request interrupted"} {
		if strings.HasPrefix(s, p) {
			return false
		}
	}
	return true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
