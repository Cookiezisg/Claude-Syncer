// Package desktop reads and writes Claude Desktop's on-disk session records.
//
// Layout (Claude Desktop for macOS, verified on 6.6395.1):
//
//	~/Library/Application Support/Claude/
//	  config.json                         lastKnownAccountUuid = signed-in account
//	  claude-code-sessions/<account>/<org>/
//	    local_<id>.json                   one session record ("card")
//	    deleted_<id>                      tombstone; content is a ms timestamp;
//	                                      written for the card id and the CLI id
//	    archived-sessions.idx             {"v":1,"archived":["local_<id>",...]},
//	                                      a load-order hint
//	    scheduled-tasks.json, waiting-input/   per-account, never synced
//
// The app keeps the signed-in account's records in memory and writes them
// back when it switches account, so that account's folders may only gain new
// files while it is signed in. See docs/desktop-internals.md.
package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/jsonobj"
)

const (
	cardPrefix   = "local_"
	cardSuffix   = ".json"
	markerPrefix = "deleted_"
	archiveIndex = "archived-sessions.idx"

	// MaxCardSize mirrors the app's own limit: it treats larger records as
	// damaged and only harvests their ids.
	MaxCardSize = 10 << 20

	filePerm fs.FileMode = 0o600
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether s is a lowercase canonical UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// Layout locates Claude Desktop's files.
type Layout struct {
	AppSupport string // ~/Library/Application Support/Claude
}

// DefaultLayout is the standard macOS location.
func DefaultLayout() Layout {
	home, _ := os.UserHomeDir()
	return Layout{AppSupport: filepath.Join(home, "Library", "Application Support", "Claude")}
}

// SessionsRoot is the claude-code-sessions directory.
func (l Layout) SessionsRoot() string { return filepath.Join(l.AppSupport, "claude-code-sessions") }

// ConfigPath is the app's config.json.
func (l Layout) ConfigPath() string { return filepath.Join(l.AppSupport, "config.json") }

// ActiveAccount returns the signed-in account uuid, or "" if unknown.
func (l Layout) ActiveAccount() (string, error) {
	data, err := os.ReadFile(l.ConfigPath())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var cfg struct {
		LastKnownAccountUUID string `json:"lastKnownAccountUuid"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("config.json: %w", err)
	}
	return cfg.LastKnownAccountUUID, nil
}

// Folder is one account/org session directory.
type Folder struct {
	Account string
	Org     string
	Dir     string
}

// Key identifies the folder in state and logs.
func (f Folder) Key() string { return f.Account + "/" + f.Org }

// Short is a compact label for humans.
func (f Folder) Short() string { return short(f.Account) + "/" + short(f.Org) }

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Folders lists every <account>/<org> directory under the sessions root.
func (l Layout) Folders() ([]Folder, error) {
	root := l.SessionsRoot()
	accounts, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Folder
	for _, a := range accounts {
		if !a.IsDir() || !IsUUID(a.Name()) {
			continue
		}
		orgs, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			return nil, err
		}
		for _, o := range orgs {
			if !o.IsDir() || !IsUUID(o.Name()) {
				continue
			}
			out = append(out, Folder{Account: a.Name(), Org: o.Name(), Dir: filepath.Join(root, a.Name(), o.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

// Card is one session record file.
type Card struct {
	Folder  Folder
	Name    string // file name without .json, e.g. local_<uuid>
	Obj     *jsonobj.Object
	ModTime time.Time
	Size    int64
}

// Path is the card file's path.
func (c *Card) Path() string { return filepath.Join(c.Folder.Dir, c.Name+cardSuffix) }

// LocalID is the id after "local_".
func (c *Card) LocalID() string { return strings.TrimPrefix(c.Name, cardPrefix) }

// ClaimIDs are every CLI session id the card holds a claim on. The app checks
// the same five fields when deciding which transcripts a record owns.
func (c *Card) ClaimIDs() []string { return ClaimIDs(c.Obj) }

// ClaimIDs returns the CLI session ids claimed by a record.
func ClaimIDs(o *jsonobj.Object) []string {
	var ids []string
	for _, k := range []string{"cliSessionId", "unarchivedCliSessionId", "preClearCliSessionId"} {
		if s, ok := o.String(k); ok && s != "" {
			ids = append(ids, s)
		}
	}
	ids = append(ids, o.Strings("priorCliSessionIds")...)
	ids = append(ids, o.Strings("overflowCliSessionIds")...)
	return ids
}

// IsRemote reports whether the record is an SSH or WSL session. The app keeps
// those out of local transcript bookkeeping, and so do we.
func (c *Card) IsRemote() bool { return c.Obj.IsTruthy("sshConfig") || c.Obj.IsTruthy("wslConfig") }

// FolderData is everything read from one folder.
type FolderData struct {
	Folder   Folder
	Cards    []*Card
	Markers  map[string]int64 // tombstone id -> ms timestamp (0 if unreadable)
	Archived []string         // archived-sessions.idx entries, nil if absent
	HasIndex bool
	Problems []string // records we could not use, for status/logs
	// ProblemIDs are UUIDs found in records we could not use. They still
	// count as claims, so we never import or remove a transcript a damaged
	// record might own. The app fails closed the same way.
	ProblemIDs []string
}

// ReadFolder reads one folder. A record that cannot be parsed or is too
// large is reported in Problems and left untouched.
func ReadFolder(f Folder) (*FolderData, error) {
	fsutil.CleanStaleTemps(f.Dir)
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return nil, err
	}
	fd := &FolderData{Folder: f, Markers: map[string]int64{}}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		switch {
		case strings.HasPrefix(name, markerPrefix):
			id := strings.TrimPrefix(name, markerPrefix)
			b, _ := os.ReadFile(filepath.Join(f.Dir, name))
			ts, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			fd.Markers[id] = ts
		case name == archiveIndex:
			ids, err := readArchiveIndex(filepath.Join(f.Dir, name))
			if err != nil {
				fd.Problems = append(fd.Problems, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			fd.Archived, fd.HasIndex = ids, true
		case strings.HasPrefix(name, cardPrefix) && strings.HasSuffix(name, cardSuffix):
			c, err := readCard(f, name)
			if err != nil {
				fd.Problems = append(fd.Problems, fmt.Sprintf("%s/%s: %v", f.Short(), name, err))
				fd.ProblemIDs = append(fd.ProblemIDs, harvestIDs(filepath.Join(f.Dir, name))...)
				continue
			}
			fd.Cards = append(fd.Cards, c)
		}
	}
	return fd, nil
}

func readCard(f Folder, name string) (*Card, error) {
	path := filepath.Join(f.Dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxCardSize {
		return nil, fmt.Errorf("oversized (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, err := jsonobj.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("unparsable: %w", err)
	}
	c := &Card{Folder: f, Name: strings.TrimSuffix(name, cardSuffix), Obj: obj, ModTime: info.ModTime(), Size: info.Size()}
	if err := Validate(c.Name, obj); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate checks the fields the sync relies on. A record that fails is
// left alone: if the app changed its format we must not guess.
func Validate(name string, o *jsonobj.Object) error {
	id, ok := o.String("sessionId")
	if !ok {
		return errors.New("no sessionId")
	}
	if id != name {
		return fmt.Errorf("sessionId %q does not match file name", id)
	}
	if raw, ok := o.Get("title"); ok && string(raw) != "null" {
		if _, ok := o.String("title"); !ok {
			return errors.New("title is not a string")
		}
	}
	if o.Has("isArchived") {
		if _, ok := o.Bool("isArchived"); !ok {
			return errors.New("isArchived is not a boolean")
		}
	}
	if raw, ok := o.Get("cliSessionId"); ok && string(raw) != "null" {
		if _, ok := o.String("cliSessionId"); !ok {
			return errors.New("cliSessionId is not a string")
		}
	}
	return nil
}

var anyUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func harvestIDs(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, 64<<20))
	return anyUUID.FindAllString(string(data), -1)
}

func readArchiveIndex(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx struct {
		V        int      `json:"v"`
		Archived []string `json:"archived"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if idx.V != 1 {
		return nil, fmt.Errorf("unknown version %d", idx.V)
	}
	return idx.Archived, nil
}

// WriteCard atomically replaces (or creates) a card file.
func WriteCard(dir, name string, obj *jsonobj.Object) error {
	return fsutil.WriteFileAtomic(filepath.Join(dir, name+cardSuffix), obj.Marshal(), filePerm)
}

// CreateCard writes a card only if no file of that name exists. Used in the
// signed-in account's folders, where we may add but never replace.
func CreateCard(dir, name string, obj *jsonobj.Object) (bool, error) {
	return fsutil.CreateExclusive(filepath.Join(dir, name+cardSuffix), obj.Marshal(), filePerm)
}

// CardPath is the path a card of that name has in dir.
func CardPath(dir, name string) string { return filepath.Join(dir, name+cardSuffix) }

// WriteMarker creates a tombstone if none exists.
func WriteMarker(dir, id string, ms int64) (bool, error) {
	return fsutil.CreateExclusive(filepath.Join(dir, markerPrefix+id), []byte(strconv.FormatInt(ms, 10)), filePerm)
}

// WriteArchiveIndex replaces archived-sessions.idx.
func WriteArchiveIndex(dir string, names []string) error {
	if names == nil {
		names = []string{}
	}
	data, err := json.Marshal(struct {
		V        int      `json:"v"`
		Archived []string `json:"archived"`
	}{1, names})
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, archiveIndex), data, filePerm)
}

// CardName is the file name stem for a local id.
func CardName(localID string) string { return cardPrefix + localID }
