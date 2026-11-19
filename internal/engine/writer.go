package engine

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
	"github.com/Cookiezisg/Claude-Syncer/internal/jsonobj"
	"github.com/Cookiezisg/Claude-Syncer/internal/transcript"
)

// writer applies decisions to the folders, honouring the add-only rule, and
// keeps each folder's in-memory card list matching the disk so that state is
// recorded from what is really there.
type writer struct {
	o    Options
	r    *Report
	runs []*folderRun
	now  func() time.Time
	// inUse are CLI ids claimed by a live record (or one we cannot read, or
	// an SSH/WSL record). A deletion never tombstones or removes them.
	inUse map[string]bool
	// hold: folder key + "/" + record name not to recreate yet.
	hold map[string]bool
}

func (w *writer) change(kind string, f desktop.Folder, target, note string) {
	w.r.Changes = append(w.r.Changes, Change{Kind: kind, Folder: f.Short(), Target: target, Note: note})
}

func (w *writer) problem(format string, args ...any) {
	w.r.Problems = append(w.r.Problems, fmt.Sprintf(format, args...))
}

func cardsOf(s *session, fr *folderRun) []*desktop.Card {
	var out []*desktop.Card
	for _, rp := range s.reps {
		if rp.card.Folder.Key() == fr.data.Folder.Key() {
			out = append(out, rp.card)
		}
	}
	return out
}

func titleOf(o *jsonobj.Object) string {
	t, _ := o.String("title")
	if len([]rune(t)) > 40 {
		t = string([]rune(t)[:39]) + "…"
	}
	return fmt.Sprintf("%q", t)
}

// spread makes every folder hold the session's agreed content.
func (w *writer) spread(s *session) {
	for _, fr := range w.runs {
		f := fr.data.Folder
		cards := cardsOf(s, fr)
		if len(cards) == 0 {
			if w.hold[f.Key()+"/"+s.name] {
				// Just disappeared here; its tombstone may be on the way.
				continue
			}
			// Adding is allowed everywhere, signed-in account included.
			obj := fresh(s.name, s.want)
			kind := "create"
			if s.imported {
				kind = "import"
			}
			w.change(kind, f, s.name, titleOf(obj))
			if w.o.DryRun {
				continue
			}
			ok, err := desktop.CreateCard(f.Dir, s.name, obj)
			if err != nil {
				w.problem("%s: creating %s: %v", f.Short(), s.name, err)
				continue
			}
			if !ok {
				// Something appeared under that name since we read the
				// folder; leave it for the next pass.
				continue
			}
			fr.data.Cards = append(fr.data.Cards, &desktop.Card{Folder: f, Name: s.name, Obj: obj, ModTime: w.now()})
			continue
		}
		for _, c := range cards {
			next := apply(s.want, c.Obj)
			if bytes.Equal(next.Marshal(), c.Obj.Marshal()) {
				continue
			}
			if !fr.writable {
				w.r.Pending++
				continue
			}
			w.change("update", f, c.Name, changedFields(c.Obj, next))
			if w.o.DryRun {
				continue
			}
			if err := desktop.WriteCard(f.Dir, c.Name, next); err != nil {
				w.problem("%s: writing %s: %v", f.Short(), c.Name, err)
				continue
			}
			c.Obj = next
		}
	}
}

func changedFields(old, next *jsonobj.Object) string {
	var keys []string
	for _, k := range next.Keys() {
		a, _ := old.Get(k)
		b, _ := next.Get(k)
		if !bytes.Equal(a, b) {
			keys = append(keys, k)
		}
	}
	for _, k := range old.Keys() {
		if !next.Has(k) {
			keys = append(keys, "-"+k)
		}
	}
	if len(keys) > 4 {
		keys = append(keys[:4], fmt.Sprintf("+%d", len(keys)-4))
	}
	return strings.Join(keys, ",")
}

// removable counts the records a deletion would remove now.
func (w *writer) removable(s *session) int {
	n := 0
	for _, fr := range w.runs {
		if fr.writable {
			n += len(cardsOf(s, fr))
		}
	}
	return n
}

// delete carries a deletion to every folder: the records go (to the trash)
// where we may remove them, tombstones for every id of the session go
// everywhere so nothing re-imports it, and once no record is left anywhere
// the transcript goes too, as the app does when only one account has it.
func (w *writer) delete(s *session, tombstones map[string]int64, byID map[string]transcript.File, recordsClean bool) {
	ts := int64(0)
	for id := range s.ids {
		if t := tombstones[id]; t > ts {
			ts = t
		}
	}
	if ts == 0 {
		ts = w.now().UnixMilli()
	}
	// Tombstone the record id and every transcript only this session claims,
	// so neither the app nor our import brings any of them back.
	var ids []string
	for _, id := range sortedKeys(s.ids) {
		if id == s.key || !w.inUse[id] {
			ids = append(ids, id)
		}
	}
	title := ""
	if len(s.reps) > 0 {
		title = titleOf(s.reps[0].card.Obj)
	}

	left := 0
	for _, fr := range w.runs {
		f := fr.data.Folder
		for _, c := range cardsOf(s, fr) {
			if !fr.writable {
				left++
				w.r.Pending++
				continue
			}
			w.change("remove", f, c.Name, title)
			if w.o.DryRun {
				continue
			}
			if _, err := w.o.Trash.Put(c.Path(), w.o.Layout.SessionsRoot()); err != nil {
				w.problem("%s: removing %s: %v", f.Short(), c.Name, err)
				left++
				continue
			}
			fr.data.Cards = removeCard(fr.data.Cards, c)
		}
		var added int
		for _, id := range ids {
			if _, ok := fr.data.Markers[id]; ok {
				continue
			}
			added++
			if w.o.DryRun {
				continue
			}
			if _, err := desktop.WriteMarker(f.Dir, id, ts); err != nil {
				w.problem("%s: tombstone %s: %v", f.Short(), id, err)
				continue
			}
			fr.data.Markers[id] = ts
		}
		if added > 0 {
			w.change("tombstone", f, s.name, fmt.Sprintf("%d id(s)", added))
		}
	}

	if left > 0 || !w.o.DeleteTranscripts || !recordsClean {
		return
	}
	for _, id := range ids {
		f, ok := byID[id]
		if !ok || w.inUse[id] {
			continue
		}
		if age := w.now().Sub(f.ModTime); age < w.o.QuietPeriod {
			// Possibly still being written; try again on a later pass.
			continue
		}
		w.r.Changes = append(w.r.Changes, Change{Kind: "transcript", Folder: "~/.claude", Target: id + ".jsonl", Note: title})
		if w.o.DryRun {
			continue
		}
		home, _ := os.UserHomeDir()
		if _, err := w.o.Trash.Put(f.Path, home); err != nil {
			w.problem("removing transcript %s: %v", id, err)
			continue
		}
		if d := transcriptDir(f); exists(d) {
			w.o.Trash.Put(d, home)
		}
	}
}

func removeCard(cards []*desktop.Card, c *desktop.Card) []*desktop.Card {
	out := cards[:0]
	for _, x := range cards {
		if x != c {
			out = append(out, x)
		}
	}
	return out
}

// reindex keeps archived-sessions.idx equal to the folder's archived
// records, as the app keeps it.
func (w *writer) reindex(fr *folderRun) {
	var archived []string
	for _, c := range fr.data.Cards {
		if a, _ := c.Obj.Bool("isArchived"); a {
			archived = append(archived, c.Name)
		}
	}
	sort.Strings(archived)
	have := append([]string(nil), fr.data.Archived...)
	sort.Strings(have)
	if !fr.data.HasIndex && len(archived) == 0 {
		return
	}
	if fr.data.HasIndex && strings.Join(have, ",") == strings.Join(archived, ",") {
		return
	}
	w.change("index", fr.data.Folder, "archived-sessions.idx", fmt.Sprintf("%d archived", len(archived)))
	if w.o.DryRun {
		return
	}
	if err := desktop.WriteArchiveIndex(fr.data.Folder.Dir, archived); err != nil {
		w.problem("%s: archive index: %v", fr.data.Folder.Short(), err)
		return
	}
	fr.data.Archived, fr.data.HasIndex = archived, true
}
