package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTranscriptCachedInvalidatesOnAppend(t *testing.T) {
	resetTranscriptParseCache()
	dir := filepath.Join(t.TempDir(), ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	header := `{"type":"session","id":"pi-1","timestamp":"2026-07-26T10:00:00Z","cwd":"/tmp/work"}` + "\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewScannerWithoutDaemon()
	first, hit := s.parseTranscriptCached(path)
	if hit || !first.found || first.sessionID != "pi-1" {
		t.Fatalf("first = %#v, hit=%v", first, hit)
	}
	second, hit := s.parseTranscriptCached(path)
	if !hit || second.sessionID != "pi-1" {
		t.Fatalf("second = %#v, hit=%v", second, hit)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"message","message":{"role":"assistant","content":"ok"}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, hit = s.parseTranscriptCached(path)
	if hit {
		t.Fatal("changed transcript unexpectedly used cached parse")
	}
}
