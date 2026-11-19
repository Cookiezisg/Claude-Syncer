// Package state persists what the last sync saw and decided, which is what
// lets the next sync tell an edit from a stale copy.
//
// Without it, two folders that disagree look the same whether one was just
// edited or one was just written back from the app's memory with old
// content. With it, a record whose content still matches what we last saw
// in that folder is known to be unchanged, however new its mtime.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/transcript"
)

// Version of the state file format.
const Version = 1

// State is the whole state file.
type State struct {
	Version int `json:"version"`

	// Bases: folder key -> card name -> the fields in which that card, as
	// last seen or written by us, differed from its session's agreed
	// content (field -> value hash, "" = field absent). A card in sync has an
	// empty map. Storing only differences keeps this small: after a sync
	// every writable card is in sync.
	Bases map[string]map[string]map[string]string `json:"bases"`

	// Active is the signed-in account at the last run, and ActiveSince when
	// we first saw it signed in. Right after a switch the app is still
	// writing the previous account's records back, so both accounts stay
	// add-only for a short while.
	Active      string `json:"active,omitempty"`
	PrevActive  string `json:"prevActive,omitempty"`
	ActiveSince int64  `json:"activeSince,omitempty"`

	// Sessions: session key -> what the sync decided last time.
	Sessions map[string]*Session `json:"sessions"`

	// Transcripts: CLI session id -> incremental metadata cache.
	Transcripts map[string]*transcript.Meta `json:"transcripts"`

	LastRun *RunSummary `json:"lastRun,omitempty"`
	// The version the background agent last ran, and when.
	AgentVersion string    `json:"agentVersion,omitempty"`
	AgentSeen    time.Time `json:"agentSeen,omitempty"`

	Notified map[string]int64 `json:"notified,omitempty"` // issue -> unix ms of last notification
}

// Session is the agreed content of one conversation.
type Session struct {
	IDs    []string          `json:"ids"`    // local ids and CLI ids
	Fields map[string]string `json:"fields"` // synced field -> value hash
	// Last transcript-derived titles we applied, so each change in the
	// transcript is applied once and never fights a rename in the app.
	CustomTitle string `json:"ct,omitempty"`
	AITitle     string `json:"at,omitempty"`
}

// RunSummary is shown by `claude-syncer status`.
type RunSummary struct {
	Version       string    `json:"version,omitempty"` // claude-syncer version that ran it
	Background    bool      `json:"background,omitempty"`
	At            time.Time `json:"at"`
	DurationMS    int64     `json:"durationMs"`
	Folders       int       `json:"folders"`
	Accounts      int       `json:"accounts"`
	Sessions      int       `json:"sessions"`
	ActiveAccount string    `json:"activeAccount,omitempty"`
	Changes       int       `json:"changes"`
	Pending       int       `json:"pending"`
	Problems      []string  `json:"problems,omitempty"`
	Halted        string    `json:"halted,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// New returns an empty state.
func New() *State {
	return &State{
		Version:     Version,
		Bases:       map[string]map[string]map[string]string{},
		Sessions:    map[string]*Session{},
		Transcripts: map[string]*transcript.Meta{},
		Notified:    map[string]int64{},
	}
}

// Load reads the state file. A missing file gives an empty state; a file of
// an unknown version is an error rather than something to overwrite.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	s := New()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Version != Version {
		return nil, errors.New("state file was written by a different version of claude-syncer")
	}
	if s.Bases == nil {
		s.Bases = map[string]map[string]map[string]string{}
	}
	if s.Sessions == nil {
		s.Sessions = map[string]*Session{}
	}
	if s.Transcripts == nil {
		s.Transcripts = map[string]*transcript.Meta{}
	}
	if s.Notified == nil {
		s.Notified = map[string]int64{}
	}
	return s, nil
}

// Save writes the state file atomically.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}
