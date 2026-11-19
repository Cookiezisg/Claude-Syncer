package engine

import (
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"sort"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
	"github.com/Cookiezisg/Claude-Syncer/internal/jsonobj"
)

// absent is the hash of a field that is not there.
const absent = ""

func valueHash(raw json.RawMessage) string {
	h := fnv.New64a()
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// fieldHashes maps every synced field of a record to its value hash.
func fieldHashes(o *jsonobj.Object) map[string]string {
	out := map[string]string{}
	for _, k := range o.Keys() {
		if desktop.IsSynced(k) {
			raw, _ := o.Get(k)
			out[k] = valueHash(raw)
		}
	}
	return out
}

// syncedOnly copies the synced fields of a record, in order.
func syncedOnly(o *jsonobj.Object) *jsonobj.Object {
	out := jsonobj.New()
	for _, k := range o.Keys() {
		if desktop.IsSynced(k) {
			raw, _ := o.Get(k)
			out.SetRaw(k, raw)
		}
	}
	return out
}

// diff returns the fields where have differs from want (field -> have's
// hash, absent if have lacks it). Empty means in sync.
func diff(have, want map[string]string) map[string]string {
	out := map[string]string{}
	for k, w := range want {
		if h := have[k]; h != w {
			out[k] = h
		}
	}
	for k, h := range have {
		if _, ok := want[k]; !ok {
			out[k] = h
		}
	}
	return out
}

// replica is one record of a session as seen this run.
type replica struct {
	card   *desktop.Card
	hashes map[string]string
	// base is what this record's fields were when we last looked, or nil
	// if we have never seen this record.
	base map[string]string
}

func (r *replica) changed(k string) bool {
	if r.base == nil {
		return true
	}
	return r.hashes[k] != r.base[k]
}

// merge decides a session's content field by field.
//
// prev is the content agreed last run (field -> hash), nil for a session we
// have not seen. For each field, records that changed it since we last
// looked (relative to their own base) are candidates, and the most recently
// modified one wins. A record that has not changed a field does not vote on
// it, however recent its mtime, which is what keeps a stale copy written back
// from the app's memory from undoing an edit made elsewhere. A field nobody
// changed keeps its agreed value.
//
// Archiving rewrites a record without touching lastActivityAt, so file
// mtime, not any timestamp inside the record, orders the candidates.
func merge(prev map[string]string, reps []*replica) *jsonobj.Object {
	if len(reps) == 0 {
		return nil
	}
	newest := reps[0]
	for _, r := range reps[1:] {
		if r.card.ModTime.After(newest.card.ModTime) {
			newest = r
		}
	}
	if prev == nil {
		return syncedOnly(newest.card.Obj)
	}

	keys := map[string]bool{}
	for k := range prev {
		keys[k] = true
	}
	for _, r := range reps {
		for k := range r.hashes {
			keys[k] = true
		}
	}

	// Key order: the newest record's order, then any others.
	order := []string{}
	seen := map[string]bool{}
	for _, k := range newest.card.Obj.Keys() {
		if keys[k] && !seen[k] {
			order, seen[k] = append(order, k), true
		}
	}
	var rest []string
	for k := range keys {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	out := jsonobj.New()
	for _, k := range order {
		var winner *replica
		for _, r := range reps {
			if !r.changed(k) || r.hashes[k] == prev[k] {
				continue
			}
			if winner == nil || r.card.ModTime.After(winner.card.ModTime) {
				winner = r
			}
		}
		if winner == nil {
			// Unchanged: keep the agreed value. Find a record that holds it.
			for _, r := range reps {
				if r.hashes[k] == prev[k] {
					winner = r
					break
				}
			}
		}
		if winner == nil {
			// The agreed value is held by no record (cannot normally
			// happen); fall back to the newest record.
			winner = newest
		}
		if raw, ok := winner.card.Obj.Get(k); ok {
			out.SetRaw(k, raw)
		}
	}
	return out
}

// apply returns card with its synced fields replaced by want's, keeping its
// account-local fields and key order.
func apply(want *jsonobj.Object, card *jsonobj.Object) *jsonobj.Object {
	out := card.Clone()
	for _, k := range card.Keys() {
		if desktop.IsSynced(k) && !want.Has(k) {
			out.Delete(k)
		}
	}
	for _, k := range want.Keys() {
		raw, _ := want.Get(k)
		out.SetRaw(k, raw)
	}
	return out
}

// fresh builds a new record named name from want.
func fresh(name string, want *jsonobj.Object) *jsonobj.Object {
	out := jsonobj.New()
	out.Set("sessionId", name)
	for _, k := range want.Keys() {
		raw, _ := want.Get(k)
		out.SetRaw(k, raw)
	}
	return out
}
