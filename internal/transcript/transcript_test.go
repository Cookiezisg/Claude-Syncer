package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateIsIncremental(t *testing.T) {
	dir := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	path := filepath.Join(dir, id+".jsonl")
	write := func(s string) File {
		fh, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		fh.WriteString(s)
		fh.Close()
		info, _ := os.Stat(path)
		return File{ID: id, Path: path, Dir: dir, ModTime: info.ModTime(), Size: info.Size()}
	}

	m := &Meta{}
	f := write(`{"type":"user","entrypoint":"cli","cwd":"/p","timestamp":"2026-11-01T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>"},{"type":"text","text":"  real\nquestion "}]}}` + "\n")
	if err := Update(m, f); err != nil {
		t.Fatal(err)
	}
	if !m.HasUserMessage || m.FirstUserText != "real question" || m.Entrypoint != "cli" || m.CWD != "/p" {
		t.Fatalf("meta = %+v", m)
	}
	first := m.Offset

	// A partial line is not consumed until it is complete.
	f = write(`{"type":"ai-title","aiTitle":"Gen`)
	Update(m, f)
	if m.Offset != first || m.AITitle != "" {
		t.Fatalf("consumed a partial line: %+v", m)
	}
	f = write(`erated"}` + "\n" + `{"type":"custom-title","customTitle":"Mine"}` + "\n")
	Update(m, f)
	if m.AITitle != "Generated" || m.CustomTitle != "Mine" || m.Title() != "Mine" {
		t.Fatalf("meta = %+v", m)
	}
}

func TestToolResultsAreNotUserMessages(t *testing.T) {
	dir := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	path := filepath.Join(dir, id+".jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"out"}]},"toolUseResult":{"x":1}}`+"\n"+
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}`+"\n"), 0o600)
	info, _ := os.Stat(path)
	m := &Meta{}
	Update(m, File{ID: id, Path: path, ModTime: info.ModTime(), Size: info.Size()})
	if m.HasUserMessage {
		t.Errorf("counted a tool result or meta line as a user message: %+v", m)
	}
}
