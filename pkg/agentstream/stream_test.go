package agentstream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// plainNormalizer maps one JSONL line {"id":..,"text":..} to one entry, so
// stream mechanics can be asserted without provider-format ceremony.
type plainNormalizer struct{}

func (plainNormalizer) NormalizeLine(line []byte) (*transcript.UnifiedEntry, error) {
	var raw struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	return &transcript.UnifiedEntry{
		Role:      "user",
		MessageID: raw.ID,
		Parts:     []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: raw.Text}}},
	}, nil
}

func (plainNormalizer) Provider() string { return "test" }

func line(id, text string) string {
	return `{"id":"` + id + `","text":"` + text + `"}` + "\n"
}

// collectUntilCheckpoint drains events until a checkpoint arrives, returning
// the entry IDs seen, the checkpoint offset, and whether a reset was seen.
func collectUntilCheckpoint(t *testing.T, events <-chan Event) (ids []string, offset int64, reset bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before checkpoint; entries so far %v", ids)
			}
			if event.Reset {
				reset = true
				continue
			}
			if event.Entry != nil {
				ids = append(ids, event.Entry.MessageID)
				continue
			}
			return ids, event.Offset, reset
		case <-deadline:
			t.Fatalf("timed out waiting for checkpoint; entries so far %v", ids)
		}
	}
}

func streamEventsAt(t *testing.T, path string, startOffset int64) (<-chan Event, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events, err := StreamEvents(ctx, StreamOptions{TranscriptPath: path, Normalizer: plainNormalizer{}, StartOffset: startOffset})
	if err != nil {
		t.Fatal(err)
	}
	return events, cancel
}

func TestStreamEventsResumeFromOffsetSkipsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(line("m1", "one")+line("m2", "two")), 0o600); err != nil {
		t.Fatal(err)
	}

	events, cancel := streamEventsAt(t, path, 0)
	ids, offset, reset := collectUntilCheckpoint(t, events)
	cancel()
	if reset || len(ids) != 2 || ids[0] != "m1" || ids[1] != "m2" {
		t.Fatalf("initial stream: ids=%v reset=%v", ids, reset)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Fatalf("checkpoint offset = %d, want file size %d", offset, info.Size())
	}

	appendFile(t, path, line("m3", "three")+line("m4", "four"))

	events, cancel = streamEventsAt(t, path, offset)
	ids, resumed, reset := collectUntilCheckpoint(t, events)
	cancel()
	if reset {
		t.Fatal("resume at a valid offset signalled a reset")
	}
	if len(ids) != 2 || ids[0] != "m3" || ids[1] != "m4" {
		t.Fatalf("resumed stream replayed history: ids=%v", ids)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if resumed != info.Size() {
		t.Fatalf("resumed checkpoint = %d, want %d", resumed, info.Size())
	}
}

func TestStreamEventsTruncationFallsBackToFullReplayWithReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(line("m1", "one")+line("m2", "two")+line("m3", "three")), 0o600); err != nil {
		t.Fatal(err)
	}
	events, cancel := streamEventsAt(t, path, 0)
	_, offset, _ := collectUntilCheckpoint(t, events)
	cancel()

	// Rewrite the file shorter than the saved offset.
	if err := os.WriteFile(path, []byte(line("n1", "fresh")), 0o600); err != nil {
		t.Fatal(err)
	}

	events, cancel = streamEventsAt(t, path, offset)
	ids, _, reset := collectUntilCheckpoint(t, events)
	cancel()
	if !reset {
		t.Fatal("truncated file resumed without a reset event")
	}
	if len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("post-reset replay ids=%v, want [n1]", ids)
	}
}

func TestStreamEventsRewriteBreakingLineBoundaryResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(line("m1", "one")), 0o600); err != nil {
		t.Fatal(err)
	}
	events, cancel := streamEventsAt(t, path, 0)
	_, offset, _ := collectUntilCheckpoint(t, events)
	cancel()

	// Rewrite the file at least as long as before, but with no newline at the
	// saved boundary: the offset must be rejected, not trusted.
	longer := line("r1", "rewritten-and-much-longer-than-the-original-line")
	if int64(len(longer)) <= offset {
		t.Fatalf("fixture bug: rewrite (%d bytes) not longer than offset %d", len(longer), offset)
	}
	if err := os.WriteFile(path, []byte(longer), 0o600); err != nil {
		t.Fatal(err)
	}

	events, cancel = streamEventsAt(t, path, offset)
	ids, _, reset := collectUntilCheckpoint(t, events)
	cancel()
	if !reset {
		t.Fatal("rewritten file resumed mid-line without a reset event")
	}
	if len(ids) != 1 || ids[0] != "r1" {
		t.Fatalf("post-reset replay ids=%v, want [r1]", ids)
	}
}

func TestStreamEventsCheckpointStopsAtPartialTrailingLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	full := line("m1", "complete")
	partial := `{"id":"m2","text":"split-`
	if err := os.WriteFile(path, []byte(full+partial), 0o600); err != nil {
		t.Fatal(err)
	}

	events, cancel := streamEventsAt(t, path, 0)
	defer cancel()
	ids, offset, reset := collectUntilCheckpoint(t, events)
	if reset || len(ids) != 1 || ids[0] != "m1" {
		t.Fatalf("partial line leaked an entry: ids=%v reset=%v", ids, reset)
	}
	if offset != int64(len(full)) {
		t.Fatalf("checkpoint offset = %d, want line boundary %d", offset, len(full))
	}

	// Complete the split line. Parsing "m2" out of it proves the prefix bufio
	// consumed before EOF was kept, and the checkpoint must advance to the new
	// boundary.
	appendFile(t, path, `across-writes"}`+"\n")
	ids, offset, reset = collectUntilCheckpoint(t, events)
	if reset || len(ids) != 1 || ids[0] != "m2" {
		t.Fatalf("completed split line: ids=%v reset=%v", ids, reset)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Fatalf("checkpoint offset = %d, want %d", offset, info.Size())
	}
}

func TestStreamDropsEnvelopeAndKeepsHistoricalShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(line("m1", "one")+line("m2", "two")), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, err := Stream(ctx, StreamOptions{TranscriptPath: path, Normalizer: plainNormalizer{}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	deadline := time.After(5 * time.Second)
	for len(ids) < 2 {
		select {
		case entry, ok := <-entries:
			if !ok {
				t.Fatalf("stream closed early; ids=%v", ids)
			}
			ids = append(ids, entry.MessageID)
		case <-deadline:
			t.Fatalf("timed out; ids=%v", ids)
		}
	}
	if ids[0] != "m1" || ids[1] != "m2" {
		t.Fatalf("ids=%v", ids)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}
