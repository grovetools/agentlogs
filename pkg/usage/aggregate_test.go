package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

func entry(msgID, reqID string, sidechain bool, input, output int) loadedEntry {
	return loadedEntry{
		MessageID:   msgID,
		RequestID:   reqID,
		IsSidechain: sidechain,
		Model:       "claude-opus-4-5",
		Usage:       transcript.Usage{InputTokens: input, OutputTokens: output},
	}
}

func TestDedupeExactKeyCollapses(t *testing.T) {
	// Same (message.id, request_id) appears twice — collapses to one.
	in := []loadedEntry{
		entry("m1", "r1", false, 100, 10),
		entry("m1", "r1", false, 100, 10),
		entry("m2", "r2", false, 200, 20),
	}
	out := dedupe(in)
	if len(out) != 2 {
		t.Fatalf("dedupe kept %d, want 2", len(out))
	}
}

func TestDedupeDistinctRequestIDsSurvive(t *testing.T) {
	// Same message id but distinct request ids and neither is a sidechain:
	// both survive (they are genuinely different billed responses).
	in := []loadedEntry{
		entry("m1", "r1", false, 100, 10),
		entry("m1", "r2", false, 150, 15),
	}
	out := dedupe(in)
	if len(out) != 2 {
		t.Fatalf("dedupe kept %d, want 2 (distinct request ids)", len(out))
	}
}

func TestDedupeSidechainReplayCollapses(t *testing.T) {
	// A sidechain replay of the same message id (new request id) collapses into
	// the original via the sidechain fallback, keeping the non-sidechain copy.
	in := []loadedEntry{
		entry("m1", "r1", false, 100, 10), // original
		entry("m1", "r2", true, 100, 10),  // sidechain replay
	}
	out := dedupe(in)
	if len(out) != 1 {
		t.Fatalf("dedupe kept %d, want 1 (sidechain replay)", len(out))
	}
	if out[0].IsSidechain {
		t.Error("should keep the non-sidechain copy")
	}
}

func TestShouldReplacePrefersNonSidechainThenHigherTotal(t *testing.T) {
	nonSide := entry("m", "r", false, 100, 0)
	side := entry("m", "r", true, 999, 0)
	if shouldReplace(side, nonSide) {
		t.Error("sidechain candidate should not replace non-sidechain existing")
	}
	if !shouldReplace(nonSide, side) {
		t.Error("non-sidechain candidate should replace sidechain existing")
	}

	low := entry("m", "r", false, 100, 0)
	high := entry("m", "r", false, 500, 0)
	if !shouldReplace(high, low) {
		t.Error("higher-total candidate should replace lower-total existing")
	}
	if shouldReplace(low, high) {
		t.Error("lower-total candidate should not replace higher-total existing")
	}
}

// writeTranscript writes JSONL lines to a file, creating parent dirs.
func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func usageLine(sessionID, msgID, reqID string, input, output int) string {
	return `{"type":"assistant","sessionId":"` + sessionID + `","requestId":"` + reqID +
		`","timestamp":"2026-01-01T00:00:00.000Z","message":{"id":"` + msgID +
		`","model":"claude-opus-4-5","usage":{"input_tokens":` + itoa(input) +
		`,"output_tokens":` + itoa(output) + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestSummarizeSessionRollup builds a fixture project slug with a parent
// transcript, an ad-hoc root agent-*.jsonl (inner sessionId == parent), and a
// workflow agent under <sid>/subagents/workflows/wf_*/, and asserts the rollup
// includes all three.
func TestSummarizeSessionRollup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	projects := filepath.Join(root, "projects")
	slug := filepath.Join(projects, "-Users-me-proj")
	sid := "sess-123"

	// Parent transcript: 1 message.
	writeTranscript(t, filepath.Join(slug, sid+".jsonl"),
		usageLine(sid, "p1", "pr1", 1000, 100),
	)
	// Ad-hoc root agent whose inner sessionId == parent sid: 1 message.
	writeTranscript(t, filepath.Join(slug, "agent-aaa111.jsonl"),
		usageLine(sid, "a1", "ar1", 2000, 200),
	)
	// Workflow agent under sid/subagents/workflows/wf_x/: 1 message.
	writeTranscript(t, filepath.Join(slug, sid, "subagents", "workflows", "wf_x", "agent-bbb222.jsonl"),
		usageLine(sid, "w1", "wr1", 3000, 300),
	)
	// Unrelated agent (different inner sessionId) must be excluded.
	writeTranscript(t, filepath.Join(slug, "agent-ccc333.jsonl"),
		usageLine("other-sid", "o1", "or1", 9999, 999),
	)

	s, err := SummarizeSession([]string{slug}, sid, CostModeCalculate)
	if err != nil {
		t.Fatal(err)
	}
	if s.Usage.Input != 1000+2000+3000 {
		t.Errorf("input=%d, want 6000 (parent+adhoc+workflow)", s.Usage.Input)
	}
	if s.Usage.Output != 100+200+300 {
		t.Errorf("output=%d, want 600", s.Usage.Output)
	}
	if s.MessageCount != 3 {
		t.Errorf("messageCount=%d, want 3", s.MessageCount)
	}
	if s.CostUSD <= 0 {
		t.Errorf("cost=%g, want > 0", s.CostUSD)
	}
	// The unrelated agent's tokens must not leak in.
	if s.Usage.Input >= 9999 {
		t.Errorf("unrelated agent leaked into rollup: input=%d", s.Usage.Input)
	}
}

// TestScanProjectsGroupsByProjectAndSession verifies the gate scanner groups by
// the (projectPath, sessionId) composite and drops zero-token sessions.
func TestScanProjectsGroupsByProjectAndSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	slugA := filepath.Join(root, "projects", "-proj-a")
	slugB := filepath.Join(root, "projects", "-proj-b")

	// Each standalone .jsonl file is its own session (path-derived).
	writeTranscript(t, filepath.Join(slugA, "s1.jsonl"), usageLine("s1", "m1", "r1", 100, 10))
	writeTranscript(t, filepath.Join(slugB, "s1.jsonl"), usageLine("s1", "m2", "r2", 200, 20))
	// A zero-token session must be dropped.
	writeTranscript(t, filepath.Join(slugA, "empty.jsonl"), usageLine("empty", "m3", "r3", 0, 0))

	res, err := ScanProjects([]string{slugA, slugB}, CostModeCalculate, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Two non-empty sessions (same sessionId "s1" but different project paths).
	if len(res.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(res.Sessions))
	}
	if res.Totals.Usage.Input != 300 || res.Totals.Usage.Output != 30 {
		t.Errorf("totals input=%d output=%d, want 300/30", res.Totals.Usage.Input, res.Totals.Usage.Output)
	}
}
