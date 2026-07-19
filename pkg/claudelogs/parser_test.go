package claudelogs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeLines writes the given lines (each newline-terminated) to path and
// returns the resulting file size in bytes.
func writeLines(t *testing.T, path string, lines ...string) int64 {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return fileSize(t, path)
}

// appendRaw appends raw bytes (no newline added) to path.
func appendRaw(t *testing.T, path, raw string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(raw); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}

func messageIDs(msgs []transcript.ExtractedMessage) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.MessageID)
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// userLine builds a well-formed "user" transcript line with a string content body.
func userLine(uuid, text string) string {
	return `{"type":"user","sessionId":"sess-off","uuid":"` + uuid +
		`","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}`
}

// ---------------------------------------------------------------------------
// ParseFile
// ---------------------------------------------------------------------------

// TestParseFileWellFormed pins the full extraction contract over a hand-built
// fixture: which entry shapes yield an ExtractedMessage and which are dropped,
// plus every field of the extracted values.
func TestParseFileWellFormed(t *testing.T) {
	p := NewParser()
	msgs, err := p.ParseFile(filepath.Join("testdata", "wellformed.jsonl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// The fixture has 7 entries. Only 3 survive extraction:
	//   - u1 user, string content
	//   - u2 assistant, one text block
	//   - u5 assistant, two text blocks joined by "\n"
	// Dropped: u3 (tool_use only -> empty text), u4 (tool_result only ->
	// empty text), the "summary" entry (type not user/assistant), and u6
	// (message.type != "message" so the assistant branch rejects it).
	wantIDs := []string{"user_u1", "msg_001", "msg_003"}
	if got := messageIDs(msgs); !equalStrings(got, wantIDs) {
		t.Fatalf("MessageIDs = %v, want %v", got, wantIDs)
	}

	// --- message 0: user with a bare-string content body --------------------
	m0 := msgs[0]
	if m0.SessionID != "sess-abc" {
		t.Errorf("m0.SessionID = %q, want sess-abc", m0.SessionID)
	}
	// User entries carry no message.id, so the parser synthesises
	// "<entry.Type>_<entry.UUID>".
	if m0.MessageID != "user_u1" {
		t.Errorf("m0.MessageID = %q, want user_u1", m0.MessageID)
	}
	if m0.Role != "user" {
		t.Errorf("m0.Role = %q, want user", m0.Role)
	}
	if m0.Content != "Hello Claude" {
		t.Errorf("m0.Content = %q, want %q", m0.Content, "Hello Claude")
	}
	if string(m0.RawContent) != `"Hello Claude"` {
		t.Errorf("m0.RawContent = %q, want the raw JSON string literal", string(m0.RawContent))
	}
	wantTS := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !m0.Timestamp.Equal(wantTS) {
		t.Errorf("m0.Timestamp = %v, want %v", m0.Timestamp, wantTS)
	}
	// A user entry has no model/usage/stop_reason, so only the three
	// always-present keys are set.
	if _, ok := m0.Metadata["model"]; ok {
		t.Errorf("m0.Metadata unexpectedly has model: %v", m0.Metadata["model"])
	}
	if _, ok := m0.Metadata["usage"]; ok {
		t.Errorf("m0.Metadata unexpectedly has usage")
	}
	if m0.Metadata["uuid"] != "u1" {
		t.Errorf("m0.Metadata[uuid] = %v, want u1", m0.Metadata["uuid"])
	}
	if m0.Metadata["parent_uuid"] != "" {
		t.Errorf("m0.Metadata[parent_uuid] = %v, want empty", m0.Metadata["parent_uuid"])
	}
	if m0.Metadata["user_type"] != "external" {
		t.Errorf("m0.Metadata[user_type] = %v, want external", m0.Metadata["user_type"])
	}

	// --- message 1: assistant with model/usage/stop_reason metadata ---------
	m1 := msgs[1]
	if m1.Role != "assistant" {
		t.Errorf("m1.Role = %q, want assistant", m1.Role)
	}
	if m1.Content != "Hi there" {
		t.Errorf("m1.Content = %q, want %q", m1.Content, "Hi there")
	}
	if m1.Metadata["model"] != "claude-opus-4-8" {
		t.Errorf("m1.Metadata[model] = %v, want claude-opus-4-8", m1.Metadata["model"])
	}
	if m1.Metadata["stop_reason"] != "end_turn" {
		t.Errorf("m1.Metadata[stop_reason] = %v, want end_turn", m1.Metadata["stop_reason"])
	}
	usage, ok := m1.Metadata["usage"].(*transcript.Usage)
	if !ok {
		t.Fatalf("m1.Metadata[usage] type = %T, want *transcript.Usage", m1.Metadata["usage"])
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 5 || usage.CacheReadInputTokens != 100 {
		t.Errorf("usage = %+v, want input=10 output=5 cacheRead=100", *usage)
	}
	// RawContent is the raw message.content JSON, not the whole line.
	if !strings.HasPrefix(string(m1.RawContent), `[{"type":"text"`) {
		t.Errorf("m1.RawContent = %q, want the raw content array", string(m1.RawContent))
	}

	// --- message 2: multiple text blocks are joined with a newline ----------
	if msgs[2].Content != "Part one\nPart two" {
		t.Errorf("m2.Content = %q, want %q", msgs[2].Content, "Part one\nPart two")
	}
}

// TestParseFileMalformedLinesDegrade pins that a bad line is skipped rather
// than aborting the parse: messages both BEFORE and AFTER the damage survive,
// and no error is returned.
func TestParseFileMalformedLinesDegrade(t *testing.T) {
	p := NewParser()
	msgs, err := p.ParseFile(filepath.Join("testdata", "malformed.jsonl"))
	if err != nil {
		t.Fatalf("ParseFile returned an error for a partially-malformed file: %v", err)
	}

	wantIDs := []string{"user_m1", "msg_bad_1"}
	if got := messageIDs(msgs); !equalStrings(got, wantIDs) {
		t.Fatalf("MessageIDs = %v, want %v (bad lines skipped, good lines kept)", got, wantIDs)
	}
	if msgs[0].Content != "before the damage" {
		t.Errorf("msgs[0].Content = %q", msgs[0].Content)
	}
	if msgs[1].Content != "after the damage" {
		t.Errorf("msgs[1].Content = %q", msgs[1].Content)
	}
}

// TestParseFileMissingFile pins the clean-error path for a nonexistent file.
func TestParseFileMissingFile(t *testing.T) {
	p := NewParser()
	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")

	msgs, err := p.ParseFile(missing)
	if err == nil {
		t.Fatal("ParseFile on a missing file returned nil error")
	}
	if msgs != nil {
		t.Errorf("ParseFile on a missing file returned %d messages, want nil", len(msgs))
	}
	if !strings.Contains(err.Error(), "failed to open file") {
		t.Errorf("error = %q, want it wrapped with %q", err.Error(), "failed to open file")
	}
	if !os.IsNotExist(unwrapAll(err)) {
		t.Errorf("error does not unwrap to fs.ErrNotExist: %v", err)
	}
}

func TestParseFileEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := NewParser().ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile on empty file: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages from empty file, want 0", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ParseFileFromOffset — the incremental-read contract the monitor depends on
// ---------------------------------------------------------------------------

// TestParseFileFromOffsetResumption is the core incremental-read test: reading
// from the offset returned by a previous read must yield ONLY the newly
// appended messages, with no duplicates and no gaps.
func TestParseFileFromOffsetResumption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	size1 := writeLines(t, path,
		userLine("a1", "first"),
		userLine("a2", "second"),
	)

	p := NewParser()

	// --- pass 1: read from 0 -----------------------------------------------
	msgs1, off1, err := p.ParseFileFromOffset(path, 0)
	if err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if got := messageIDs(msgs1); !equalStrings(got, []string{"user_a1", "user_a2"}) {
		t.Fatalf("pass 1 ids = %v, want [user_a1 user_a2]", got)
	}
	// The new offset is the byte position after the last consumed line, which
	// for a fully-consumed file is the file size.
	if off1 != size1 {
		t.Fatalf("pass 1 offset = %d, want file size %d", off1, size1)
	}

	// --- pass 2: no new data -----------------------------------------------
	msgs2, off2, err := p.ParseFileFromOffset(path, off1)
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if len(msgs2) != 0 {
		t.Errorf("pass 2 returned %d messages (%v), want 0 — re-reading at EOF must not duplicate",
			len(msgs2), messageIDs(msgs2))
	}
	if off2 != off1 {
		t.Errorf("pass 2 offset = %d, want unchanged %d", off2, off1)
	}

	// --- pass 3: append two lines, resume -----------------------------------
	appendRaw(t, path, userLine("a3", "third")+"\n"+userLine("a4", "fourth")+"\n")
	size3 := fileSize(t, path)

	msgs3, off3, err := p.ParseFileFromOffset(path, off2)
	if err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if got := messageIDs(msgs3); !equalStrings(got, []string{"user_a3", "user_a4"}) {
		t.Fatalf("pass 3 ids = %v, want exactly the appended [user_a3 user_a4]", got)
	}
	if off3 != size3 {
		t.Errorf("pass 3 offset = %d, want file size %d", off3, size3)
	}

	// --- sanity: the union of the incremental reads equals a full read ------
	full, err := p.ParseFile(path)
	if err != nil {
		t.Fatalf("full ParseFile: %v", err)
	}
	var union []string
	union = append(union, messageIDs(msgs1)...)
	union = append(union, messageIDs(msgs2)...)
	union = append(union, messageIDs(msgs3)...)
	if got := messageIDs(full); !equalStrings(union, got) {
		t.Errorf("incremental union = %v, full read = %v — incremental reads lost or duplicated messages", union, got)
	}
}

// TestParseFileFromOffsetMidFileOffset pins that an offset landing on a line
// boundary skips exactly the preceding lines.
func TestParseFileFromOffsetMidFileOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line1 := userLine("b1", "first")
	writeLines(t, path, line1, userLine("b2", "second"), userLine("b3", "third"))

	// Offset just past line 1 (including its newline).
	offset := int64(len(line1) + 1)

	msgs, newOff, err := NewParser().ParseFileFromOffset(path, offset)
	if err != nil {
		t.Fatalf("ParseFileFromOffset: %v", err)
	}
	if got := messageIDs(msgs); !equalStrings(got, []string{"user_b2", "user_b3"}) {
		t.Errorf("ids = %v, want [user_b2 user_b3]", got)
	}
	if newOff != fileSize(t, path) {
		t.Errorf("newOffset = %d, want %d", newOff, fileSize(t, path))
	}
}

// TestParseFileFromOffsetTruncatedTailIsConsumed pins a REAL DATA-LOSS quirk of
// the incremental contract: a partially-written final line (no trailing
// newline) is handed to the scanner as a complete token, fails to unmarshal,
// is silently skipped — and the returned offset still advances past it. When
// the writer later completes that line, resuming from the returned offset sees
// only the tail fragment and the message is lost forever.
func TestParseFileFromOffsetTruncatedTailIsConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeLines(t, path, userLine("c1", "complete"))

	// Simulate the monitor racing a writer mid-line.
	partial := userLine("c2", "torn")
	head, tail := partial[:40], partial[40:]
	appendRaw(t, path, head)

	p := NewParser()
	msgs, off, err := p.ParseFileFromOffset(path, 0)
	if err != nil {
		t.Fatalf("torn read: %v", err)
	}
	if got := messageIDs(msgs); !equalStrings(got, []string{"user_c1"}) {
		t.Fatalf("torn read ids = %v, want [user_c1]", got)
	}
	// The offset advanced past the torn bytes even though they yielded nothing.
	sizeWithPartial := fileSize(t, path)
	if off != sizeWithPartial {
		t.Fatalf("offset after torn read = %d, want %d (offset advances past unparsed bytes)",
			off, sizeWithPartial)
	}

	// The writer finishes the line.
	appendRaw(t, path, tail+"\n")

	msgs2, _, err := p.ParseFileFromOffset(path, off)
	if err != nil {
		t.Fatalf("resume read: %v", err)
	}
	if len(msgs2) != 0 {
		t.Fatalf("resume read returned %v; this test documents that the torn message is LOST. "+
			"If the parser gained partial-line handling, update this test.", messageIDs(msgs2))
	}

	// And a full re-read does see it, proving the loss is offset-specific.
	full, err := p.ParseFile(path)
	if err != nil {
		t.Fatalf("full read: %v", err)
	}
	if got := messageIDs(full); !equalStrings(got, []string{"user_c1", "user_c2"}) {
		t.Errorf("full re-read ids = %v, want [user_c1 user_c2]", got)
	}
}

// TestParseFileFromOffsetBeyondEOF pins that an offset past the end of the file
// is not an error: seeking beyond EOF is legal, so the call returns no messages
// and echoes the (now bogus) offset back.
func TestParseFileFromOffsetBeyondEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	size := writeLines(t, path, userLine("d1", "only"))

	msgs, newOff, err := NewParser().ParseFileFromOffset(path, size+9999)
	if err != nil {
		t.Fatalf("offset beyond EOF returned an error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
	if newOff != size+9999 {
		t.Errorf("newOffset = %d, want the echoed input offset %d", newOff, size+9999)
	}
}

// TestParseFileFromOffsetMissingFile pins that the input offset is echoed back
// unchanged on an open failure (so a caller that blindly stores the returned
// offset does not corrupt its cursor).
func TestParseFileFromOffsetMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	msgs, newOff, err := NewParser().ParseFileFromOffset(missing, 1234)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if msgs != nil {
		t.Errorf("messages = %v, want nil", msgs)
	}
	if newOff != 1234 {
		t.Errorf("newOffset = %d, want the input offset 1234 echoed back", newOff)
	}
	if !strings.Contains(err.Error(), "failed to open file") {
		t.Errorf("error = %q, want %q prefix", err.Error(), "failed to open file")
	}
}

// TestParseFileFromOffsetMalformedDoesNotStall pins that malformed lines do not
// wedge the offset: the cursor still advances to EOF.
func TestParseFileFromOffsetMalformedDoesNotStall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	size := writeLines(t, path,
		userLine("e1", "good"),
		`{"type":"user","uuid":"e2","message":{`,
		userLine("e3", "also good"),
	)

	msgs, off, err := NewParser().ParseFileFromOffset(path, 0)
	if err != nil {
		t.Fatalf("ParseFileFromOffset: %v", err)
	}
	if got := messageIDs(msgs); !equalStrings(got, []string{"user_e1", "user_e3"}) {
		t.Errorf("ids = %v, want [user_e1 user_e3]", got)
	}
	if off != size {
		t.Errorf("offset = %d, want %d", off, size)
	}
}

// ---------------------------------------------------------------------------
// GetTranscriptPath
// ---------------------------------------------------------------------------

// TestGetTranscriptPath drives the real glob against a fake HOME so the test is
// hermetic (os.UserHomeDir reads $HOME on unix).
func TestGetTranscriptPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projects := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(filepath.Join(projects, "-Users-x-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(projects, "-Users-x-repo", "sess-123.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := GetTranscriptPath("sess-123")
		if err != nil {
			t.Fatalf("GetTranscriptPath: %v", err)
		}
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		got, err := GetTranscriptPath("sess-absent")
		if err == nil {
			t.Fatalf("expected an error, got path %q", got)
		}
		if got != "" {
			t.Errorf("path = %q, want empty on error", got)
		}
		// The error names both the session and the (defaulted) provider.
		if !strings.Contains(err.Error(), "transcript not found for session sess-absent") {
			t.Errorf("error = %q, want it to name the session", err.Error())
		}
		if !strings.Contains(err.Error(), "provider: claude") {
			t.Errorf("error = %q, want it to name provider claude", err.Error())
		}
	})

	t.Run("empty session id", func(t *testing.T) {
		// Globs to ".../*/.jsonl"; no such file, so this is a clean not-found.
		if _, err := GetTranscriptPath(""); err == nil {
			t.Error("expected an error for an empty session id")
		}
	})

	t.Run("glob metacharacters in session id", func(t *testing.T) {
		// The session id is interpolated straight into the glob pattern, so a
		// malformed bracket expression surfaces as filepath.ErrBadPattern
		// rather than a not-found error.
		got, err := GetTranscriptPath("sess-[123")
		if err == nil {
			t.Fatalf("expected an error, got %q", got)
		}
		if err != filepath.ErrBadPattern {
			t.Errorf("error = %v, want filepath.ErrBadPattern (session id is not escaped)", err)
		}
	})
}

// TestGetTranscriptPathMultipleMatches pins that the first lexical match wins
// when the same session id exists under two project dirs (filepath.Glob sorts).
func TestGetTranscriptPathMultipleMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projects := filepath.Join(home, ".claude", "projects")
	for _, proj := range []string{"bbb-project", "aaa-project"} {
		dir := filepath.Join(projects, proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dup.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := GetTranscriptPath("dup")
	if err != nil {
		t.Fatalf("GetTranscriptPath: %v", err)
	}
	want := filepath.Join(projects, "aaa-project", "dup.jsonl")
	if got != want {
		t.Errorf("path = %q, want the lexically first match %q", got, want)
	}
}

// unwrapAll peels every %w wrapper off an error.
func unwrapAll(err error) error {
	for {
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return err
		}
		next := u.Unwrap()
		if next == nil {
			return err
		}
		err = next
	}
}
