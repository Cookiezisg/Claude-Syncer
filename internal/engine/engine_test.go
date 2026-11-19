package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/jsonobj"
	"github.com/Cookiezisg/Claude-Syncer/internal/state"
)

// Accounts A, B, C with one org each.
const (
	acctA = "aaaaaaaa-0000-4000-8000-000000000000"
	acctB = "bbbbbbbb-0000-4000-8000-000000000000"
	acctC = "cccccccc-0000-4000-8000-000000000000"
	org   = "00000000-0000-4000-8000-0000000000aa"

	sess1 = "11111111-1111-4111-8111-111111111111" // local id
	cli1  = "22222222-2222-4222-8222-222222222222" // its CLI id
	cli2  = "33333333-3333-4333-8333-333333333333" // a terminal-only session
)

type fixture struct {
	t     *testing.T
	root  string
	clock time.Time
	st    *state.State
	opts  Options
}

func newFixture(t *testing.T, active string) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, clock: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), st: state.New()}
	for _, a := range []string{acctA, acctB, acctC} {
		must(t, os.MkdirAll(f.folder(a), 0o755))
	}
	must(t, os.MkdirAll(f.projects(), 0o755))
	f.setActive(active)
	f.opts = Options{
		Layout:            desktop.Layout{AppSupport: filepath.Join(root, "Claude")},
		ProjectsDir:       f.projects(),
		Trash:             &fsutil.Trash{Dir: filepath.Join(root, "trash"), Retention: 7 * 24 * time.Hour, Now: f.now},
		Now:               f.now,
		Import:            true,
		DeleteTranscripts: true,
		QuietPeriod:       10 * time.Minute,
		SwitchGrace:       60 * time.Second,
		MaxDeletes:        20,
	}
	return f
}

func (f *fixture) now() time.Time { return f.clock }

// tick advances the clock past the switch grace period.
func (f *fixture) tick(d time.Duration) { f.clock = f.clock.Add(d) }

func (f *fixture) folder(acct string) string {
	return filepath.Join(f.root, "Claude", "claude-code-sessions", acct, org)
}

func (f *fixture) projects() string { return filepath.Join(f.root, "claude", "projects") }

func (f *fixture) setActive(acct string) {
	data, _ := json.Marshal(map[string]string{"lastKnownAccountUuid": acct})
	must(f.t, os.WriteFile(filepath.Join(f.root, "Claude", "config.json"), data, 0o644))
}

// writeCard writes a card with the given fields and sets its mtime.
func (f *fixture) writeCard(acct, local string, mtime time.Time, kv ...any) {
	f.t.Helper()
	o := jsonobj.New()
	o.Set("sessionId", "local_"+local)
	for i := 0; i < len(kv); i += 2 {
		must(f.t, o.Set(kv[i].(string), kv[i+1]))
	}
	path := filepath.Join(f.folder(acct), "local_"+local+".json")
	must(f.t, os.WriteFile(path, o.Marshal(), 0o600))
	must(f.t, os.Chtimes(path, mtime, mtime))
}

func (f *fixture) card(acct, local string) *jsonobj.Object {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.folder(acct), "local_"+local+".json"))
	if err != nil {
		return nil
	}
	o, err := jsonobj.Parse(data)
	must(f.t, err)
	return o
}

func (f *fixture) title(acct, local string) string {
	o := f.card(acct, local)
	if o == nil {
		return "<missing>"
	}
	s, _ := o.String("title")
	return s
}

func (f *fixture) cardsIn(acct string) []string {
	entries, _ := os.ReadDir(f.folder(acct))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "local_") {
			out = append(out, e.Name())
		}
	}
	return out
}

func (f *fixture) hasMarker(acct, id string) bool {
	_, err := os.Stat(filepath.Join(f.folder(acct), "deleted_"+id))
	return err == nil
}

// writeTranscript writes a transcript made of the given JSON lines.
func (f *fixture) writeTranscript(id string, mtime time.Time, lines ...string) string {
	f.t.Helper()
	dir := filepath.Join(f.projects(), "-tmp-proj")
	must(f.t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, id+".jsonl")
	must(f.t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	must(f.t, os.Chtimes(path, mtime, mtime))
	return path
}

func (f *fixture) appendTranscript(id string, mtime time.Time, lines ...string) {
	f.t.Helper()
	path := filepath.Join(f.projects(), "-tmp-proj", id+".jsonl")
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	must(f.t, err)
	fh.WriteString(strings.Join(lines, "\n") + "\n")
	fh.Close()
	must(f.t, os.Chtimes(path, mtime, mtime))
}

func userLine(id, entry, text, ts string) string {
	return fmt.Sprintf(`{"type":"user","sessionId":%q,"entrypoint":%q,"cwd":"/tmp/proj","timestamp":%q,"message":{"role":"user","content":%q}}`, id, entry, ts, text)
}

func (f *fixture) run() *Report {
	f.t.Helper()
	rep, err := Run(f.opts, f.st)
	must(f.t, err)
	return rep
}

// settle runs passes until nothing changes, failing if it never does.
func (f *fixture) settle() {
	f.t.Helper()
	for i := 0; i < 3; i++ {
		if rep := f.run(); len(rep.Changes) == 0 {
			return
		}
	}
	f.t.Fatalf("sync does not converge: %v", f.run().Changes)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
