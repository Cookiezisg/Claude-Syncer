package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A session made in one account appears in every account, without the
// account's own fields, and a second pass changes nothing.
func TestCreateSpreadsWithoutAccountFields(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "hello", "isArchived", false,
		"remoteMcpServersConfig", []string{"big"}, "bridgeSessionIds", []string{"x"})

	f.run()
	for _, a := range []string{acctA, acctC} {
		c := f.card(a, sess1)
		if c == nil {
			t.Fatalf("not created in %s", a)
		}
		if c.Has("remoteMcpServersConfig") || c.Has("bridgeSessionIds") {
			t.Errorf("account-local fields copied to %s: %s", a, c.Marshal())
		}
		if got, _ := c.String("title"); got != "hello" {
			t.Errorf("%s title = %q", a, got)
		}
	}
	if !f.card(acctB, sess1).Has("remoteMcpServersConfig") {
		t.Error("source lost its own account fields")
	}
	f.settle()
	if rep := f.run(); len(rep.Changes) != 0 {
		t.Errorf("second pass changed things: %v", rep.Changes)
	}
}

// A rename spreads to inactive accounts at once and to the signed-in
// account after it is no longer signed in.
func TestRenameWaitsForSignedInAccount(t *testing.T) {
	f := newFixture(t, acctA)
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "old")
	}
	f.settle()

	f.tick(time.Minute)
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "new")
	rep := f.run()
	if got := f.title(acctC, sess1); got != "new" {
		t.Errorf("C title = %q", got)
	}
	if got := f.title(acctA, sess1); got != "old" {
		t.Errorf("signed-in A was modified: %q", got)
	}
	if rep.Pending != 1 {
		t.Errorf("pending = %d, want 1", rep.Pending)
	}

	// Switch to B. A is protected for the grace period, then updated.
	f.setActive(acctB)
	f.tick(time.Second)
	f.run()
	if got := f.title(acctA, sess1); got != "old" {
		t.Errorf("A updated during grace period: %q", got)
	}
	f.tick(2 * time.Minute)
	f.run()
	if got := f.title(acctA, sess1); got != "new" {
		t.Errorf("A title after switch = %q", got)
	}
}

// When the app writes the previously signed-in account's records back from
// memory, the stale (but newer-by-mtime) copy must not undo an edit made
// elsewhere.
func TestStaleWriteBackDoesNotRevert(t *testing.T) {
	f := newFixture(t, acctA)
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "old", "isArchived", false)
	}
	f.settle()

	// Renamed in B while A is signed in.
	f.tick(time.Minute)
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "new", "isArchived", false)
	f.run()

	// Switch away from A: the app rewrites A's record, unchanged, newer mtime.
	f.tick(time.Minute)
	f.setActive(acctC)
	f.writeCard(acctA, sess1, f.clock.Add(time.Hour), "cliSessionId", cli1, "title", "old", "isArchived", false)
	f.tick(2 * time.Minute)
	f.run()
	f.tick(2 * time.Minute)
	f.settle()

	for _, a := range []string{acctA, acctB, acctC} {
		if got := f.title(a, sess1); got != "new" {
			t.Errorf("%s title = %q, want new", a, got)
		}
	}
}

// Two accounts change different fields of the same session: both survive.
func TestConcurrentEditsMergeByField(t *testing.T) {
	f := newFixture(t, acctA)
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "old", "isArchived", false)
	}
	f.settle()
	f.setActive(acctC)
	f.tick(2 * time.Minute)
	f.settle()

	f.tick(time.Minute)
	f.writeCard(acctA, sess1, f.clock, "cliSessionId", cli1, "title", "renamed", "isArchived", false)
	f.writeCard(acctB, sess1, f.clock.Add(time.Second), "cliSessionId", cli1, "title", "old", "isArchived", true)
	f.run()

	for _, a := range []string{acctA, acctB} {
		c := f.card(a, sess1)
		title, _ := c.String("title")
		arch, _ := c.Bool("isArchived")
		if title != "renamed" || !arch {
			t.Errorf("%s: title=%q archived=%v", a, title, arch)
		}
	}
}

// Archiving keeps archived-sessions.idx in step.
func TestArchiveUpdatesIndex(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "t", "isArchived", true)
	f.run()
	data, err := os.ReadFile(filepath.Join(f.folder(acctC), "archived-sessions.idx"))
	must(t, err)
	if want := `{"v":1,"archived":["local_` + sess1 + `"]}`; string(data) != want {
		t.Errorf("idx = %s", data)
	}
	if _, err := os.Stat(filepath.Join(f.folder(acctA), "archived-sessions.idx")); err == nil {
		t.Error("index written in the signed-in account")
	}
}

// Deleting in the signed-in account (the app removes the record and writes
// tombstones for the record id and CLI id) deletes it everywhere, and the
// transcript once it is quiet.
func TestDeleteSpreads(t *testing.T) {
	f := newFixture(t, acctA)
	transcript := f.writeTranscript(cli1, f.clock, userLine(cli1, "cli", "hi", "2026-11-01T11:00:00Z"))
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "doomed")
	}
	f.settle()

	// The app deletes in A.
	os.Remove(filepath.Join(f.folder(acctA), "local_"+sess1+".json"))
	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+sess1), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+cli1), []byte("1"), 0o600)
	f.tick(time.Hour)
	f.run()

	for _, a := range []string{acctB, acctC} {
		if f.card(a, sess1) != nil {
			t.Errorf("record still in %s", a)
		}
		if !f.hasMarker(a, sess1) || !f.hasMarker(a, cli1) {
			t.Errorf("tombstones missing in %s", a)
		}
	}
	if _, err := os.Stat(transcript); err == nil {
		t.Error("transcript not removed")
	}
	if fsutilSize(f.opts.Trash.Dir) == 0 {
		t.Error("nothing went to the trash")
	}
	// And nothing brings it back.
	f.settle()
	if f.card(acctB, sess1) != nil {
		t.Error("session came back")
	}
}

// A deletion made elsewhere reaches the signed-in account's folder only
// after the switch; its transcript waits until then too.
func TestDeleteWaitsForSignedInAccount(t *testing.T) {
	f := newFixture(t, acctA)
	transcript := f.writeTranscript(cli1, f.clock, userLine(cli1, "cli", "hi", "2026-11-01T11:00:00Z"))
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "doomed")
	}
	f.settle()
	os.Remove(filepath.Join(f.folder(acctB), "local_"+sess1+".json"))
	os.WriteFile(filepath.Join(f.folder(acctB), "deleted_"+sess1), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(f.folder(acctB), "deleted_"+cli1), []byte("1"), 0o600)
	f.tick(time.Hour)
	f.run()
	if f.card(acctA, sess1) == nil {
		t.Fatal("record removed from the signed-in account")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal("transcript removed while a record still holds it")
	}
	f.setActive(acctB)
	f.run() // sees the switch; A is in its grace period
	if f.card(acctA, sess1) == nil {
		t.Fatal("record removed during grace period")
	}
	f.tick(2 * time.Minute)
	f.run()
	if f.card(acctA, sess1) != nil {
		t.Error("record not removed after switch")
	}
	if _, err := os.Stat(transcript); err == nil {
		t.Error("transcript not removed after switch")
	}
}

// Many deletions at once are held back.
func TestMassDeleteIsHeld(t *testing.T) {
	f := newFixture(t, acctA)
	f.opts.MaxDeletes = 1
	ids := []string{"44444444-4444-4444-8444-444444444441", "44444444-4444-4444-8444-444444444442"}
	for _, id := range ids {
		f.writeCard(acctB, id, f.clock, "title", "x")
		os.WriteFile(filepath.Join(f.folder(acctC), "deleted_"+id), []byte("1"), 0o600)
	}
	rep := f.run()
	if rep.Halted == "" {
		t.Fatal("not held back")
	}
	for _, id := range ids {
		if f.card(acctB, id) == nil {
			t.Error("deleted despite hold")
		}
	}
	f.opts.Force = true
	f.run()
	for _, id := range ids {
		if f.card(acctB, id) != nil {
			t.Error("not deleted with --force")
		}
	}
}

// Deleting many sessions in the app is the user's doing: no brake, and the
// copies left in the signed-in account's other folders follow after a switch
// without tripping it either.
func TestMassDeleteInAppIsNotHeld(t *testing.T) {
	f := newFixture(t, acctA)
	f.opts.MaxDeletes = 1
	ids := []string{"44444444-4444-4444-8444-444444444441", "44444444-4444-4444-8444-444444444442", "44444444-4444-4444-8444-444444444443"}
	for _, id := range ids {
		for _, a := range []string{acctA, acctB, acctC} {
			f.writeCard(a, id, f.clock, "title", "x")
		}
	}
	f.settle()
	for _, id := range ids {
		os.Remove(filepath.Join(f.folder(acctA), "local_"+id+".json"))
		os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+id), []byte("1"), 0o600)
	}
	f.tick(time.Second)
	if rep := f.run(); rep.Halted != "" {
		t.Fatalf("held back: %s", rep.Halted)
	}
	for _, id := range ids {
		for _, a := range []string{acctB, acctC} {
			if f.card(a, id) != nil {
				t.Errorf("%s still in %s", id, a)
			}
		}
	}
}

// A deletion seen in another account still stops at the brake.
func TestMassDeleteFromElsewhereIsHeld(t *testing.T) {
	f := newFixture(t, acctA)
	f.opts.MaxDeletes = 1
	ids := []string{"44444444-4444-4444-8444-444444444441", "44444444-4444-4444-8444-444444444442"}
	for _, id := range ids {
		f.writeCard(acctA, id, f.clock, "title", "x")
		f.writeCard(acctB, id, f.clock, "title", "x")
		os.WriteFile(filepath.Join(f.folder(acctC), "deleted_"+id), []byte("1"), 0o600)
	}
	if rep := f.run(); rep.Halted == "" {
		t.Fatal("not held back")
	}
}

// The app removes a record a few seconds before it writes the tombstone.
// A pass in between must not put the record back.
func TestRecordRemovedBeforeTombstone(t *testing.T) {
	f := newFixture(t, acctA)
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "doomed")
	}
	f.settle()

	os.Remove(filepath.Join(f.folder(acctA), "local_"+sess1+".json"))
	f.tick(time.Second)
	f.run()
	if f.card(acctA, sess1) != nil {
		t.Fatal("record put back before its tombstone could arrive")
	}

	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+sess1), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+cli1), []byte("1"), 0o600)
	f.tick(5 * time.Second)
	f.run()
	for _, a := range []string{acctA, acctB, acctC} {
		if f.card(a, sess1) != nil {
			t.Errorf("record still in %s", a)
		}
	}
}

// A record that disappears and is never tombstoned comes back.
func TestVanishedRecordReturns(t *testing.T) {
	f := newFixture(t, acctA)
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "title", "keep")
	}
	f.settle()
	os.Remove(filepath.Join(f.folder(acctB), "local_"+sess1+".json"))
	f.tick(time.Second)
	f.run()
	if f.card(acctB, sess1) != nil {
		t.Fatal("put back at once")
	}
	f.tick(3 * time.Minute)
	f.run()
	if f.title(acctB, sess1) != "keep" {
		t.Fatal("not put back after the grace period")
	}
}

// A terminal session gets a record in every account; SDK runs, desktop
// transcripts and system-only transcripts do not.
func TestImportTerminalSessions(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeTranscript(cli2, f.clock, userLine(cli2, "cli", "fix the build", "2026-11-01T11:00:00Z"))
	f.writeTranscript("55555555-5555-4555-8555-555555555551", f.clock, userLine("x", "sdk-cli", "auto", "2026-11-01T11:00:00Z"))
	f.writeTranscript("55555555-5555-4555-8555-555555555552", f.clock, userLine("x", "claude-desktop", "app", "2026-11-01T11:00:00Z"))
	f.writeTranscript("55555555-5555-4555-8555-555555555553", f.clock, userLine("x", "cli", "<command-name>/clear</command-name>", "2026-11-01T11:00:00Z"))
	f.run()

	local := importID(cli2)
	for _, a := range []string{acctA, acctB, acctC} {
		if got := f.cardsIn(a); len(got) != 1 {
			t.Fatalf("%s has %v", a, got)
		}
		c := f.card(a, local)
		if c == nil {
			t.Fatalf("no import in %s", a)
		}
		title, _ := c.String("title")
		adopted, _ := c.Bool("adoptedFromOtherSurface")
		fromCLI, _ := c.Bool("fromCliTranscript")
		if title != "fix the build" || !adopted || !fromCLI {
			t.Errorf("%s: %s", a, c.Marshal())
		}
	}
	f.settle()
}

// /rename in the terminal renames the session in every account; a later
// rename in the app is not overridden by the old terminal title.
func TestTerminalRename(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeTranscript(cli2, f.clock, userLine(cli2, "cli", "first", "2026-11-01T11:00:00Z"))
	f.run()
	f.settle()
	local := importID(cli2)

	f.tick(time.Minute)
	f.appendTranscript(cli2, f.clock, `{"type":"custom-title","customTitle":"Named","sessionId":"`+cli2+`"}`)
	f.run()
	if got := f.title(acctB, local); got != "Named" {
		t.Errorf("B title = %q", got)
	}

	// Rename in the app (B); the terminal title must not win it back.
	f.tick(time.Minute)
	c := f.card(acctB, local)
	c.Set("title", "App name")
	path := filepath.Join(f.folder(acctB), "local_"+local+".json")
	os.WriteFile(path, c.Marshal(), 0o600)
	os.Chtimes(path, f.clock, f.clock)
	f.settle()
	if got := f.title(acctC, local); got != "App name" {
		t.Errorf("C title = %q", got)
	}
}

// Distinct records that claim the same transcript (a fork, a /clear chain)
// are distinct sessions: neither may overwrite the other.
func TestRecordsSharingTranscriptStayDistinct(t *testing.T) {
	f := newFixture(t, acctA)
	other := "66666666-6666-4666-8666-666666666666"
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "original")
	f.writeCard(acctB, other, f.clock.Add(time.Minute), "priorCliSessionIds", []string{cli1}, "cliSessionId", "77777777-7777-4777-8777-777777777777", "title", "fork")
	rep := f.run()
	if rep.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", rep.Sessions)
	}
	for _, a := range []string{acctA, acctC} {
		if got := f.title(a, sess1); got != "original" {
			t.Errorf("%s original title = %q", a, got)
		}
		if got := f.title(a, other); got != "fork" {
			t.Errorf("%s fork title = %q", a, got)
		}
	}
}

// Deleting one of two sessions that share a transcript keeps the other
// session and the transcript.
func TestDeleteKeepsSharedTranscript(t *testing.T) {
	f := newFixture(t, acctA)
	other := "66666666-6666-4666-8666-666666666666"
	transcript := f.writeTranscript(cli1, f.clock, userLine(cli1, "cli", "hi", "2026-11-01T11:00:00Z"))
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "doomed")
		f.writeCard(a, other, f.clock, "priorCliSessionIds", []string{cli1}, "title", "survivor")
	}
	f.settle()
	os.Remove(filepath.Join(f.folder(acctA), "local_"+sess1+".json"))
	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+sess1), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(f.folder(acctA), "deleted_"+cli1), []byte("1"), 0o600)
	f.tick(time.Hour)
	f.run()
	if f.card(acctB, sess1) != nil {
		t.Error("deleted session still in B")
	}
	if got := f.title(acctB, other); got != "survivor" {
		t.Errorf("survivor in B = %q", got)
	}
	if f.hasMarker(acctB, cli1) {
		t.Error("tombstoned a transcript another session uses")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Error("removed a transcript another session uses")
	}
}

// An unreadable record is left alone and blocks nothing else.
func TestUnreadableRecordLeftAlone(t *testing.T) {
	f := newFixture(t, acctA)
	bad := filepath.Join(f.folder(acctB), "local_"+sess1+".json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	f.writeCard(acctB, "88888888-8888-4888-8888-888888888888", f.clock, "title", "fine")
	rep := f.run()
	if len(rep.Problems) != 1 {
		t.Errorf("problems = %v", rep.Problems)
	}
	if data, _ := os.ReadFile(bad); string(data) != "{not json" {
		t.Error("unreadable record was modified")
	}
	if f.card(acctC, "88888888-8888-4888-8888-888888888888") == nil {
		t.Error("other sessions not synced")
	}
}

// A dry run writes nothing.
func TestDryRun(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeCard(acctB, sess1, f.clock, "cliSessionId", cli1, "title", "x")
	f.opts.DryRun = true
	rep := f.run()
	if len(rep.Changes) == 0 {
		t.Fatal("dry run reported nothing")
	}
	if len(f.cardsIn(acctC)) != 0 {
		t.Error("dry run wrote")
	}
}

func fsutilSize(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// Terminal activity moves a terminal-born session's lastActivityAt, never
// an app session's, and a pass after the first changes nothing.
func TestTerminalActivity(t *testing.T) {
	f := newFixture(t, acctA)
	f.writeTranscript(cli1, f.clock, userLine(cli1, "claude-desktop", "app chat", "2026-11-01T11:00:00Z"),
		userLine(cli1, "claude-desktop", "much later", "2026-11-01T11:50:00Z"))
	for _, a := range []string{acctA, acctB, acctC} {
		f.writeCard(a, sess1, f.clock, "cliSessionId", cli1, "title", "app", "lastActivityAt", 1)
	}
	f.writeTranscript(cli2, f.clock, userLine(cli2, "cli", "term", "2026-11-01T11:00:00Z"))
	f.settle()
	local := importID(cli2)
	before, _ := f.card(acctB, local).Int("lastActivityAt")

	f.tick(time.Hour)
	f.appendTranscript(cli2, f.clock, userLine(cli2, "cli", "again", "2026-11-01T12:30:00Z"))
	f.settle()
	if got, _ := f.card(acctB, local).Int("lastActivityAt"); got <= before {
		t.Errorf("terminal session lastActivityAt not moved: %d", got)
	}
	if got, _ := f.card(acctB, sess1).Int("lastActivityAt"); got != 1 {
		t.Errorf("app session lastActivityAt changed to %d", got)
	}
}
