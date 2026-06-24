package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func be(t string, input, output int64) blockEntry {
	return blockEntry{
		Timestamp: ts(t),
		Usage:     Usage{Input: input, Output: output},
		Model:     "claude-opus-4-5",
		CostUSD:   float64(input+output) * 0.00001,
	}
}

// TestFloorToHour verifies block starts are floored to the UTC hour.
func TestFloorToHour(t *testing.T) {
	got := floorToHour(ts("2026-01-01T03:47:12Z"))
	want := ts("2026-01-01T03:00:00Z")
	if !got.Equal(want) {
		t.Errorf("floorToHour = %v, want %v", got, want)
	}
}

// TestSingleBlockWithinWindow groups entries inside one 5-hour window into a
// single block, started at the floored hour of the first entry.
func TestSingleBlockWithinWindow(t *testing.T) {
	entries := []blockEntry{
		be("2026-01-01T01:10:00Z", 100, 10),
		be("2026-01-01T02:30:00Z", 200, 20),
		be("2026-01-01T03:00:00Z", 300, 30),
	}
	now := ts("2026-01-10T00:00:00Z") // far future => inactive
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if !b.StartTime.Equal(ts("2026-01-01T01:00:00Z")) {
		t.Errorf("start=%v, want 01:00", b.StartTime)
	}
	if b.EntryCount != 3 {
		t.Errorf("entryCount=%d, want 3", b.EntryCount)
	}
	if b.Usage.Input != 600 || b.Usage.Output != 60 {
		t.Errorf("usage=%+v, want input 600 output 60", b.Usage)
	}
	if b.IsActive {
		t.Error("block should be inactive (now is far past window)")
	}
}

// TestNewBlockAfterFiveHoursSinceStart opens a second block when an entry is
// more than the window past the block start, even with no long gap.
func TestNewBlockAfterFiveHoursSinceStart(t *testing.T) {
	entries := []blockEntry{
		be("2026-01-01T01:00:00Z", 100, 10),
		be("2026-01-01T03:00:00Z", 100, 10),
		// 5h1m after the 01:00 start -> new block (since_start > duration),
		// but only ~3h after the previous entry -> no gap block.
		be("2026-01-01T06:01:00Z", 100, 10),
	}
	now := ts("2026-01-10T00:00:00Z")
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	for _, b := range blocks {
		if b.IsGap {
			t.Errorf("unexpected gap block at %v", b.StartTime)
		}
	}
	if blocks[0].EntryCount != 2 || blocks[1].EntryCount != 1 {
		t.Errorf("entry split = %d/%d, want 2/1", blocks[0].EntryCount, blocks[1].EntryCount)
	}
}

// TestGapBlockOnLongIdle inserts a gap block when the idle stretch since the
// last entry exceeds the window.
func TestGapBlockOnLongIdle(t *testing.T) {
	entries := []blockEntry{
		be("2026-01-01T01:00:00Z", 100, 10),
		// 6h after the previous entry -> since_last > duration -> close block
		// AND insert a gap block.
		be("2026-01-01T07:00:00Z", 100, 10),
	}
	now := ts("2026-01-10T00:00:00Z")
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3 (block, gap, block)", len(blocks))
	}
	if blocks[0].IsGap || !blocks[1].IsGap || blocks[2].IsGap {
		t.Errorf("gap layout wrong: %v/%v/%v", blocks[0].IsGap, blocks[1].IsGap, blocks[2].IsGap)
	}
	// Gap starts at last entry + duration, ends at the next entry.
	gap := blocks[1]
	if !gap.StartTime.Equal(ts("2026-01-01T06:00:00Z")) {
		t.Errorf("gap start=%v, want 06:00 (last+5h)", gap.StartTime)
	}
	if !gap.EndTime.Equal(ts("2026-01-01T07:00:00Z")) {
		t.Errorf("gap end=%v, want 07:00 (next entry)", gap.EndTime)
	}
}

// TestActiveBlockDetection marks the final block active when its last entry is
// younger than the window and now is before the block end.
func TestActiveBlockDetection(t *testing.T) {
	start := ts("2026-01-01T10:00:00Z")
	entries := []blockEntry{
		{Timestamp: start.Add(10 * time.Minute), Usage: Usage{Input: 100, Output: 10}, Model: "claude-opus-4-5"},
		{Timestamp: start.Add(20 * time.Minute), Usage: Usage{Input: 100, Output: 10}, Model: "claude-opus-4-5"},
	}
	now := start.Add(30 * time.Minute) // within window, just after last entry
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if !blocks[0].IsActive {
		t.Error("block should be active (now within window, last entry recent)")
	}
	if ab := ActiveBlock(blocks); ab == nil {
		t.Error("ActiveBlock returned nil for an active block")
	}
}

// TestActiveBlockStaleWhenLastEntryOld marks the block inactive once the last
// entry is older than the window — the "stale / will re-cache" signal.
func TestActiveBlockStaleWhenLastEntryOld(t *testing.T) {
	entries := []blockEntry{
		be("2026-01-01T10:10:00Z", 100, 10),
	}
	now := ts("2026-01-01T16:00:00Z") // > 5h after the only entry
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].IsActive {
		t.Error("block should be stale/inactive when last entry is older than window")
	}
	if ActiveBlock(blocks) != nil {
		t.Error("ActiveBlock should be nil when nothing is active")
	}
}

// TestCalculateBurnRate checks tokens/min and cost/hr over the entry span.
func TestCalculateBurnRate(t *testing.T) {
	start := ts("2026-01-01T10:00:00Z")
	entries := []blockEntry{
		{Timestamp: start, Usage: Usage{Input: 500, Output: 100, CacheRead: 400}, CostUSD: 0.10},
		{Timestamp: start.Add(10 * time.Minute), Usage: Usage{Input: 500, Output: 100, CacheRead: 400}, CostUSD: 0.10},
	}
	now := start.Add(11 * time.Minute)
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	b := blocks[0]
	within := blockEntriesWithin(entries, b.StartTime, b.EndTime)
	rate := CalculateBurnRate(b, within)
	if rate == nil {
		t.Fatal("burn rate nil")
	}
	// total tokens = 2*(500+100+400)=2000 over 10 minutes = 200 tok/min.
	if rate.TokensPerMinute != 200 {
		t.Errorf("tokens/min=%g, want 200", rate.TokensPerMinute)
	}
	// non-cache = 2*(500+100)=1200 over 10 min = 120.
	if rate.TokensPerMinuteForIndicator != 120 {
		t.Errorf("indicator tok/min=%g, want 120", rate.TokensPerMinuteForIndicator)
	}
	// cost/hr = 0.20 / 10 * 60 = 1.20.
	if rate.CostPerHour < 1.19 || rate.CostPerHour > 1.21 {
		t.Errorf("cost/hr=%g, want ~1.20", rate.CostPerHour)
	}
}

// TestBurnRateNilOnZeroSpan returns nil when all entries share a timestamp.
func TestBurnRateNilOnZeroSpan(t *testing.T) {
	start := ts("2026-01-01T10:00:00Z")
	entries := []blockEntry{
		{Timestamp: start, Usage: Usage{Input: 100}},
		{Timestamp: start, Usage: Usage{Input: 100}},
	}
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, start.Add(time.Minute))
	if r := CalculateBurnRate(blocks[0], entries); r != nil {
		t.Errorf("burn rate should be nil for zero span, got %+v", r)
	}
}

// TestProjectBlockUsage extrapolates the burn rate to the block end.
func TestProjectBlockUsage(t *testing.T) {
	start := ts("2026-01-01T10:00:00Z")
	entries := []blockEntry{
		{Timestamp: start, Usage: Usage{Input: 600}, CostUSD: 0.10},
		{Timestamp: start.Add(10 * time.Minute), Usage: Usage{Input: 600}, CostUSD: 0.10},
	}
	// now = 10 min into a 5h block => 290 min remaining.
	now := start.Add(10 * time.Minute)
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	b := blocks[0]
	if !b.IsActive {
		t.Fatal("block should be active for projection")
	}
	within := blockEntriesWithin(entries, b.StartTime, b.EndTime)
	proj := ProjectBlockUsage(b, within, now)
	if proj == nil {
		t.Fatal("projection nil")
	}
	// burn = 1200 tokens / 10 min = 120 tok/min; remaining = 290 min.
	// projected = 1200 + 120*290 = 36000.
	if proj.TotalTokens != 36000 {
		t.Errorf("projected tokens=%d, want 36000", proj.TotalTokens)
	}
	if proj.RemainingMinutes != 290 {
		t.Errorf("remaining minutes=%d, want 290", proj.RemainingMinutes)
	}
}

// TestProjectInactiveBlockNil returns nil for an inactive block.
func TestProjectInactiveBlockNil(t *testing.T) {
	entries := []blockEntry{be("2026-01-01T01:00:00Z", 100, 10)}
	now := ts("2026-01-10T00:00:00Z")
	blocks := IdentifySessionBlocks(entries, 5*time.Hour, now)
	if p := ProjectBlockUsage(blocks[0], entries, now); p != nil {
		t.Errorf("projection should be nil for inactive block, got %+v", p)
	}
}

// TestEvaluateLimit classifies usage against a config denominator.
func TestEvaluateLimit(t *testing.T) {
	cases := []struct {
		name      string
		current   int64
		projected int64
		limit     int64
		want      LimitState
	}{
		{"no limit", 5000, 9000, 0, LimitOK},
		{"ok", 100, 500, 1000, LimitOK},
		{"warning", 100, 850, 1000, LimitWarning},
		{"exceeds", 100, 1200, 1000, LimitExceeded},
		{"current drives when no projection", 900, 0, 1000, LimitWarning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := EvaluateLimit(c.current, c.projected, c.limit)
			if st.State != c.want {
				t.Errorf("state=%s, want %s (pct=%.1f)", st.State, c.want, st.PercentUsed)
			}
		})
	}
}

// TestBuildBlockReportsAttachesActiveMetrics confirms burn/projection attach
// only to the active block.
func TestBuildBlockReportsAttachesActiveMetrics(t *testing.T) {
	start := ts("2026-01-01T10:00:00Z")
	entries := []blockEntry{
		{Timestamp: start, Usage: Usage{Input: 600}, CostUSD: 0.10},
		{Timestamp: start.Add(10 * time.Minute), Usage: Usage{Input: 600}, CostUSD: 0.10},
	}
	now := start.Add(10 * time.Minute)
	reports := BuildBlockReports(entries, 5*time.Hour, now)
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	if reports[0].Burn == nil || reports[0].Projection == nil {
		t.Error("active block report should carry burn rate and projection")
	}
}

// TestEmptyEntriesNoBlocks returns nil for no input.
func TestEmptyEntriesNoBlocks(t *testing.T) {
	if b := IdentifySessionBlocks(nil, 5*time.Hour, time.Now()); b != nil {
		t.Errorf("want nil for empty input, got %v", b)
	}
}

// usageLineAt is usageLine with a caller-supplied RFC3339 timestamp.
func usageLineAt(sessionID, msgID, reqID, tsRFC3339 string, input, output int) string {
	return `{"type":"assistant","sessionId":"` + sessionID + `","requestId":"` + reqID +
		`","timestamp":"` + tsRFC3339 + `","message":{"id":"` + msgID +
		`","model":"claude-opus-4-5","usage":{"input_tokens":` + itoa(input) +
		`,"output_tokens":` + itoa(output) + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
}

// TestSessionBlocksEndToEnd writes a fixture session (parent + ad-hoc agent +
// workflow agent) whose messages span more than one 5-hour window, and asserts
// SessionBlocks rolls all files in and splits them into the expected blocks.
func TestSessionBlocksEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	slug := filepath.Join(root, "projects", "-Users-me-proj")
	sid := "sess-blk"

	// Parent: two messages in the first window.
	writeTranscript(t, filepath.Join(slug, sid+".jsonl"),
		usageLineAt(sid, "p1", "pr1", "2026-01-01T01:00:00.000Z", 1000, 100),
		usageLineAt(sid, "p2", "pr2", "2026-01-01T02:00:00.000Z", 1000, 100),
	)
	// Ad-hoc agent: a message ~7h later -> a new block (with a gap between).
	writeTranscript(t, filepath.Join(slug, "agent-aaa.jsonl"),
		usageLineAt(sid, "a1", "ar1", "2026-01-01T09:00:00.000Z", 2000, 200),
	)
	// Workflow agent: another message inside that second window.
	writeTranscript(t, filepath.Join(slug, sid, "subagents", "workflows", "wf_x", "agent-bbb.jsonl"),
		usageLineAt(sid, "w1", "wr1", "2026-01-01T10:00:00.000Z", 3000, 300),
	)

	reports, err := SessionBlocks([]string{slug}, sid, CostModeCalculate, 5*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Expect: block(parent), gap, block(agent+workflow).
	var blocks, gaps int
	var totalTokens int64
	for _, r := range reports {
		if r.Block.IsGap {
			gaps++
			continue
		}
		blocks++
		totalTokens += r.Block.TotalTokens()
	}
	if blocks != 2 {
		t.Errorf("got %d non-gap blocks, want 2", blocks)
	}
	if gaps != 1 {
		t.Errorf("got %d gap blocks, want 1", gaps)
	}
	// All four messages must be included: 1100+1100+2200+3300 = 7700.
	if totalTokens != 7700 {
		t.Errorf("total tokens across blocks=%d, want 7700", totalTokens)
	}
}
