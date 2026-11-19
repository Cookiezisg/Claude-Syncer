// Package engine runs one sync pass.
//
// A pass reads every account/org folder and every transcript, groups records
// into sessions, decides each session's content, and writes it everywhere it
// may. It is idempotent: a pass over synced data writes nothing, which is
// what lets launchd run it on every file change without feedback loops.
package engine

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/jsonobj"
	"github.com/Cookiezisg/Claude-Syncer/internal/state"
	"github.com/Cookiezisg/Claude-Syncer/internal/transcript"
)

// Options configure a pass.
type Options struct {
	Layout      desktop.Layout
	ProjectsDir string // ~/.claude/projects
	Trash       *fsutil.Trash
	DryRun      bool
	Now         func() time.Time

	// Import turns terminal sessions into desktop records.
	Import bool
	// DeleteTranscripts removes a deleted session's transcript once no
	// account holds a record of it, as the app does for a single account.
	DeleteTranscripts bool
	// QuietPeriod: a transcript written to more recently is never removed.
	QuietPeriod time.Duration
	// SwitchGrace: after an account switch, both accounts stay add-only
	// this long while the app writes records back.
	SwitchGrace time.Duration
	// MaxDeletes: a pass that would delete more sessions than this deletes
	// none and reports why, unless Force is set.
	MaxDeletes int
	Force      bool
}

// Change is one write the pass made (or would make, in a dry run).
type Change struct {
	Kind   string // create, update, remove, tombstone, index, import, transcript
	Folder string
	Target string
	Note   string
}

func (c Change) String() string {
	s := fmt.Sprintf("%-10s %-17s %s", c.Kind, c.Folder, c.Target)
	if c.Note != "" {
		s += "  " + c.Note
	}
	return s
}

// Report is the outcome of a pass.
type Report struct {
	Changes  []Change
	Pending  int // records in add-only folders that differ from the agreed content
	Sessions int
	Folders  int
	Accounts int
	Active   string
	Problems []string
	Halted   string // why deletions were held back, if they were
}

type session struct {
	key      string
	ids      map[string]bool
	reps     []*replica
	prev     *state.Session
	deleted  bool
	want     *jsonobj.Object
	name     string // record name used when creating it in a folder
	imported bool
}

type folderRun struct {
	data     *desktop.FolderData
	writable bool
}

// Run performs one pass over st, updating it in place.
func Run(o Options, st *state.State) (*Report, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	r := &Report{}

	folders, err := o.Layout.Folders()
	if err != nil {
		return nil, fmt.Errorf("listing session folders: %w", err)
	}
	if len(folders) == 0 {
		return r, nil
	}
	active, err := o.Layout.ActiveAccount()
	if err != nil {
		return nil, err
	}
	r.Active = active

	// Track account switches; see Options.SwitchGrace.
	if active != st.Active {
		st.PrevActive, st.Active, st.ActiveSince = st.Active, active, now().UnixMilli()
	}
	protected := map[string]bool{active: true}
	if now().Sub(time.UnixMilli(st.ActiveSince)) < o.SwitchGrace && st.PrevActive != "" {
		protected[st.PrevActive] = true
	}

	runs := make([]*folderRun, 0, len(folders))
	accounts := map[string]bool{}
	for _, f := range folders {
		fd, err := desktop.ReadFolder(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Short(), err)
		}
		accounts[f.Account] = true
		r.Problems = append(r.Problems, fd.Problems...)
		// Unknown signed-in account: treat every folder as add-only.
		runs = append(runs, &folderRun{data: fd, writable: active != "" && !protected[f.Account]})
	}
	r.Folders, r.Accounts = len(runs), len(accounts)

	files, released, err := transcript.List(o.ProjectsDir)
	if err != nil {
		return nil, fmt.Errorf("listing transcripts: %w", err)
	}
	byID := map[string]transcript.File{}
	metas := map[string]*transcript.Meta{}
	for _, f := range files {
		byID[f.ID] = f
		m := st.Transcripts[f.ID]
		if m == nil {
			m = &transcript.Meta{}
		}
		if err := transcript.Update(m, f); err == nil {
			metas[f.ID] = m
		}
	}

	// Every id any record claims, including records we skip or could not
	// read, and every tombstoned id.
	claimed := map[string]bool{}
	remoteClaims := map[string]bool{}
	tombstones := map[string]int64{}
	for _, fr := range runs {
		for _, id := range fr.data.ProblemIDs {
			claimed[id] = true
			remoteClaims[id] = true // unknown owner: protect it the same way
		}
		for id, ts := range fr.data.Markers {
			tombstones[id] = max(tombstones[id], ts)
		}
		for _, c := range fr.data.Cards {
			claimed[c.LocalID()] = true
			for _, id := range c.ClaimIDs() {
				claimed[id] = true
				if c.IsRemote() || !desktop.IsUUID(c.LocalID()) {
					remoteClaims[id] = true
				}
			}
		}
	}

	sessions := group(runs, st)
	r.Sessions = len(sessions)

	// Decide. A session is deleted when its record id is tombstoned; the app
	// writes that tombstone on every delete (alongside one for the CLI id).
	// A CLI-id tombstone alone does not delete a record: another session may
	// share that transcript.
	for _, s := range sessions {
		if _, ok := tombstones[s.key]; ok {
			s.deleted = true
			continue
		}
		var prevFields map[string]string
		if s.prev != nil {
			prevFields = s.prev.Fields
		}
		s.want = merge(prevFields, s.reps)
		applyTranscript(s, metas)
	}

	// Import terminal sessions no record claims.
	if o.Import {
		for _, f := range files {
			if s := importable(f, metas[f.ID], claimed, tombstones, released); s != nil {
				sessions = append(sessions, s)
				r.Sessions++
			}
		}
	}

	// Transcripts a live record still claims are never tombstoned or
	// removed when some other session is deleted.
	inUse := remoteClaims
	for _, s := range sessions {
		if s.deleted {
			continue
		}
		for _, rp := range s.reps {
			for _, id := range rp.card.ClaimIDs() {
				inUse[id] = true
			}
		}
	}

	w := &writer{o: o, r: r, runs: runs, now: now, inUse: inUse}

	// Deletions, unless there are suspiciously many.
	var toDelete []*session
	for _, s := range sessions {
		if s.deleted && w.removable(s) > 0 {
			toDelete = append(toDelete, s)
		}
	}
	holdDeletes := len(toDelete) > o.MaxDeletes && !o.Force
	if holdDeletes {
		r.Halted = fmt.Sprintf("%d sessions would be deleted in one pass (limit %d); nothing was deleted. Run `claude-syncer sync --force` if that is intended.", len(toDelete), o.MaxDeletes)
	}

	for _, s := range sessions {
		switch {
		case s.deleted && !holdDeletes:
			w.delete(s, tombstones, byID, len(r.Problems) == 0)
		case s.deleted:
			// Held back: leave everything as it is.
		default:
			w.spread(s)
		}
	}
	for _, fr := range runs {
		if fr.writable {
			w.reindex(fr)
		}
	}

	if !o.DryRun {
		record(st, runs, sessions, metas)
	}
	return r, nil
}

// group collects the copies of each session across folders. A session is
// identified by its record id (local_<id>): copies in other accounts keep
// it. CLI ids do not identify sessions: several distinct records can claim
// the same transcript (a fork, a /clear chain), and merging those would
// overwrite one conversation's record with another's.
func group(runs []*folderRun, st *state.State) []*session {
	byID := map[string]*session{}
	var out []*session
	for _, fr := range runs {
		for _, c := range fr.data.Cards {
			id := c.LocalID()
			if !desktop.IsUUID(id) || c.IsRemote() {
				continue
			}
			s := byID[id]
			if s == nil {
				s = &session{key: id, name: c.Name, ids: map[string]bool{id: true}, prev: st.Sessions[id]}
				byID[id] = s
				out = append(out, s)
			}
			s.reps = append(s.reps, &replica{card: c, hashes: fieldHashes(c.Obj)})
			for _, cid := range c.ClaimIDs() {
				s.ids[cid] = true
			}
		}
	}
	for _, s := range out {
		if s.prev != nil {
			for _, id := range s.prev.IDs {
				s.ids[id] = true
			}
		}
		for _, rp := range s.reps {
			rp.base = baseOf(st, rp.card, s.prev)
		}
	}
	return out
}

// baseOf rebuilds what a record looked like when we last saw it.
func baseOf(st *state.State, c *desktop.Card, prev *state.Session) map[string]string {
	d, ok := st.Bases[c.Folder.Key()][c.Name]
	if !ok || prev == nil {
		return nil
	}
	base := map[string]string{}
	for k, v := range prev.Fields {
		base[k] = v
	}
	for k, v := range d {
		if v == absent {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	return base
}

// applyTranscript folds what changed in the session's transcript into the
// agreed content: a /rename, a generated title for a terminal-born session,
// and activity newer than the record knows about.
func applyTranscript(s *session, metas map[string]*transcript.Meta) {
	cli, _ := s.want.String("cliSessionId")
	m := metas[cli]
	if m == nil {
		return
	}
	first := s.prev == nil
	prevCustom, prevAI := "", ""
	if !first {
		prevCustom, prevAI = s.prev.CustomTitle, s.prev.AITitle
	}
	defer func() {
		// Remember what the transcript said, so each change applies once.
		if s.prev == nil {
			s.prev = &state.Session{}
		}
		s.prev.CustomTitle, s.prev.AITitle = m.CustomTitle, m.AITitle
	}()
	if first {
		// First sight: adopt the record as it is.
		return
	}
	switch {
	case m.CustomTitle != "" && m.CustomTitle != prevCustom:
		s.want.Set("title", m.CustomTitle)
		s.want.Set("titleSource", "user")
	case m.AITitle != "" && m.AITitle != prevAI:
		fromCLI, _ := s.want.Bool("fromCliTranscript")
		src, _ := s.want.String("titleSource")
		if fromCLI && src != "user" {
			s.want.Set("title", m.AITitle)
			s.want.Set("titleSource", "auto")
		}
	}
	// Activity in the terminal moves a terminal-born session up the app's
	// list. Sessions the app runs keep the app's own lastActivityAt: the app
	// maintains it, and a transcript shared by forks says nothing about
	// which fork was active.
	if fromCLI, _ := s.want.Bool("fromCliTranscript"); fromCLI {
		if last, ok := s.want.Int("lastActivityAt"); m.LastTS > 0 && (!ok || m.LastTS > last+60_000) {
			s.want.Set("lastActivityAt", m.LastTS)
		}
	}
}

// importable builds a session for a terminal transcript that no record
// claims, or returns nil if it should not get one.
func importable(f transcript.File, m *transcript.Meta, claimed map[string]bool, tombstones map[string]int64, released map[string]bool) *session {
	if m == nil || claimed[f.ID] || released[f.ID] {
		return nil
	}
	if _, dead := tombstones[f.ID]; dead {
		return nil
	}
	// Desktop-written transcripts without a record were deleted in the app;
	// SDK and `claude -p` runs are not conversations; a transcript with no
	// message from a person is a system-only run.
	if m.IsDesktop() || m.IsSDK() || !m.HasUserMessage || m.CWD == "" {
		return nil
	}
	local := importID(f.ID)
	if _, dead := tombstones[local]; dead || claimed[local] {
		return nil
	}
	created := m.FirstTS
	if created == 0 {
		created = f.ModTime.UnixMilli()
	}
	last := f.ModTime.UnixMilli()
	if m.LastTS > 0 {
		last = m.LastTS
	}
	titleSource := "auto"
	if m.CustomTitle != "" {
		titleSource = "user"
	}
	want := jsonobj.New()
	want.Set("cliSessionId", f.ID)
	want.Set("cwd", m.CWD)
	want.Set("originCwd", m.CWD)
	want.Set("createdAt", created)
	want.Set("lastActivityAt", last)
	want.Set("isArchived", false)
	want.Set("title", m.Title())
	want.Set("titleSource", titleSource)
	want.Set("permissionMode", "default")
	want.Set("adoptedFromOtherSurface", true)
	want.Set("fromCliTranscript", true)
	return &session{
		key:      local,
		ids:      map[string]bool{local: true, f.ID: true},
		want:     want,
		name:     desktop.CardName(local),
		imported: true,
		prev:     &state.Session{CustomTitle: m.CustomTitle, AITitle: m.AITitle},
	}
}

// importID derives a stable record id from a CLI session id (a UUIDv5-style
// name hash), so an import is never duplicated even if state is lost.
func importID(cli string) string {
	ns := []byte("claude-syncer/import/v1:")
	h := sha1.Sum(append(ns, cli...))
	h[6] = (h[6] & 0x0f) | 0x50
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// record writes this pass's outcome into st.
func record(st *state.State, runs []*folderRun, sessions []*session, metas map[string]*transcript.Meta) {
	st.Sessions = map[string]*state.Session{}
	wantByName := map[string]map[string]string{}
	for _, s := range sessions {
		if s.deleted || s.want == nil {
			continue
		}
		fields := fieldHashes(s.want)
		ns := &state.Session{IDs: sortedKeys(s.ids), Fields: fields}
		if s.prev != nil {
			ns.CustomTitle, ns.AITitle = s.prev.CustomTitle, s.prev.AITitle
		}
		st.Sessions[s.key] = ns
		for _, rp := range s.reps {
			wantByName[rp.card.Folder.Key()+"\x00"+rp.card.Name] = fields
		}
		wantByName["\x00"+s.name] = fields
	}
	st.Bases = map[string]map[string]map[string]string{}
	for _, fr := range runs {
		fk := fr.data.Folder.Key()
		bases := map[string]map[string]string{}
		for _, c := range fr.data.Cards {
			want, ok := wantByName[fk+"\x00"+c.Name]
			if !ok {
				want, ok = wantByName["\x00"+c.Name]
			}
			if !ok {
				continue
			}
			bases[c.Name] = diff(fieldHashes(c.Obj), want)
		}
		st.Bases[fk] = bases
	}
	st.Transcripts = metas
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// exists reports whether path exists.
func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// transcriptDir is the per-session directory (subagents, tool results) that
// sits next to a transcript.
func transcriptDir(f transcript.File) string { return filepath.Join(f.Dir, f.ID) }
