package usage

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

const piFixture = "../transcript/testdata/pi/sessions/--Users-test-project--/2026-07-01T10-00-00-000Z_0198c2f4-9a51-7abc-8def-0123456789ab.jsonl"

func TestCodexTranscriptEntries_DeltaOfTotals(t *testing.T) {
	entries, err := codexTranscriptEntries(filepath.FromSlash(codexFixture))
	if err != nil {
		t.Fatalf("codexTranscriptEntries: %v", err)
	}
	// Two usage-bearing token_count events (the info:null rate-limit event is
	// not a usage entry).
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Provider != "codex" {
			t.Errorf("Provider = %q, want codex", e.Provider)
		}
		if e.SessionID != "5973b6c0-94b8-487b-a530-2aeb6098ae0e" {
			t.Errorf("SessionID = %q (want session_meta id)", e.SessionID)
		}
		if e.ProjectPath != "/Users/dev/project" {
			t.Errorf("ProjectPath = %q (want session_meta cwd)", e.ProjectPath)
		}
		if e.Timestamp.IsZero() {
			t.Error("Timestamp should come from the rollout line")
		}
		if e.CostUSD != nil {
			t.Error("codex entries carry no native cost")
		}
	}
	// First event: totals input 1200 (1000 cached) → 200 fresh, 1000 cache read.
	if entries[0].Usage.InputTokens != 200 || entries[0].Usage.CacheReadInputTokens != 1000 || entries[0].Usage.OutputTokens != 150 {
		t.Errorf("first delta = %+v, want fresh 200 / cacheRead 1000 / out 150", entries[0].Usage)
	}
	// Second event delta: totals went 1200→3200 in (2600 cached), 150→450 out:
	// fresh 600-200=400, cacheRead 2600-1000=1600, out 300.
	if entries[1].Usage.InputTokens != 400 || entries[1].Usage.CacheReadInputTokens != 1600 || entries[1].Usage.OutputTokens != 300 {
		t.Errorf("second delta = %+v, want fresh 400 / cacheRead 1600 / out 300", entries[1].Usage)
	}
	// Deltas sum exactly to the final cumulative totals (600/2600/450).
	sum := transcript.Usage{}
	for _, e := range entries {
		sum.InputTokens += e.Usage.InputTokens
		sum.CacheReadInputTokens += e.Usage.CacheReadInputTokens
		sum.OutputTokens += e.Usage.OutputTokens
	}
	if sum.InputTokens != 600 || sum.CacheReadInputTokens != 2600 || sum.OutputTokens != 450 {
		t.Errorf("delta sum = %+v, want 600/2600/450", sum)
	}
}

func TestPiTranscriptEntries_NativeCostAllBranches(t *testing.T) {
	entries, err := piTranscriptEntries(filepath.FromSlash(piFixture))
	if err != nil {
		t.Fatalf("piTranscriptEntries: %v", err)
	}
	// Usage accounting counts every billed assistant message, INCLUDING the
	// abandoned branch (aa000004) that transcript rendering linearizes away.
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (abandoned branches are billed)", len(entries))
	}
	var total float64
	for _, e := range entries {
		if e.Provider != "pi" {
			t.Errorf("Provider = %q, want pi", e.Provider)
		}
		if e.CostUSD == nil {
			t.Fatalf("pi entry %s missing native cost", e.MessageID)
		}
		total += *e.CostUSD
		if e.SessionID != "0198c2f4-9a51-7abc-8def-0123456789ab" {
			t.Errorf("SessionID = %q (want header id)", e.SessionID)
		}
		if e.ProjectPath != "/Users/test/project" {
			t.Errorf("ProjectPath = %q (want header cwd)", e.ProjectPath)
		}
		if e.Model != "claude-sonnet-4-5" {
			t.Errorf("Model = %q", e.Model)
		}
	}
	if math.Abs(total-0.0169375) > 1e-9 {
		t.Errorf("native cost total = %v, want 0.0169375", total)
	}

	// The full summary must use the native cost verbatim (no pricing lookup).
	s, err := SummarizeSessionTranscript(filepath.FromSlash(piFixture), "pi", CostModeCalculate)
	if err != nil {
		t.Fatalf("SummarizeSessionTranscript: %v", err)
	}
	if math.Abs(s.CostUSD-0.0169375) > 1e-9 {
		t.Errorf("summary cost = %v, want native 0.0169375", s.CostUSD)
	}
	if s.MissingPricing {
		t.Error("native-cost entries must never flag MissingPricing")
	}
	if s.Provider != "pi" {
		t.Errorf("summary Provider = %q, want pi", s.Provider)
	}
	if s.Usage.Input != 3210 || s.Usage.Output != 455 {
		t.Errorf("summary usage = %+v", s.Usage)
	}
}

func TestOpenCodeUsageSource_CollectEntries(t *testing.T) {
	entries, err := collectOpenCodeEntries(filepath.FromSlash("testdata/opencode/storage"))
	if err != nil {
		t.Fatalf("collectOpenCodeEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (only token-bearing messages)", len(entries))
	}
	for _, e := range entries {
		if e.Provider != "opencode" {
			t.Errorf("Provider = %q, want opencode", e.Provider)
		}
		if e.SessionID != "ses_tok00001" {
			t.Errorf("SessionID = %q", e.SessionID)
		}
		if e.ProjectPath != "/tmp/tok-project" {
			t.Errorf("ProjectPath = %q (want session info directory)", e.ProjectPath)
		}
	}
	if entries[0].Usage.InputTokens != 100 || entries[0].Usage.CacheReadInputTokens != 200 || entries[0].Usage.CacheCreationInputTokens != 50 {
		t.Errorf("first entry usage = %+v", entries[0].Usage)
	}
}

func TestEntryCost_NativeCostPrecedence(t *testing.T) {
	pm := DefaultPricing()
	u := transcript.Usage{InputTokens: 1000, OutputTokens: 1000}
	native := 1.23

	// Native cost wins in every mode — even Calculate.
	for _, mode := range []CostMode{CostModeCalculate, CostModeAuto, CostModeDisplay} {
		cost, missing := EntryCost("claude-sonnet-4-5", u, &native, mode, pm)
		if cost != native || missing != "" {
			t.Errorf("mode %v: cost = %v missing=%q, want native %v", mode, cost, missing, native)
		}
	}

	// nil native cost keeps the historical behavior per mode.
	calc, _ := EntryCost("claude-sonnet-4-5", u, nil, CostModeCalculate, pm)
	if calc <= 0 {
		t.Errorf("calculate with nil native cost should price from the table, got %v", calc)
	}
	disp, _ := EntryCost("claude-sonnet-4-5", u, nil, CostModeDisplay, pm)
	if disp != 0 {
		t.Errorf("display with nil native cost = %v, want 0", disp)
	}
}

func TestPricingSnapshot_CoversCodexAndOpenCodeBackends(t *testing.T) {
	pm := DefaultPricing()
	for _, model := range []string{
		"gpt-5.1-codex", "gpt-5", "o3", "o4-mini", "codex-mini-latest",
		"openai/gpt-5.1-codex-mini",
		"gemini-3-pro-preview", "google/gemini-2.5-flash",
		"xai/grok-code-fast-1", "deepseek/deepseek-chat",
		"moonshotai/kimi-k2", "zai/glm-4.6",
	} {
		if _, ok := pm.Find(model); !ok {
			t.Errorf("no pricing for %q", model)
		}
	}
	// The Anthropic entries must be untouched: sonnet-4-5 stays 3/15 per 1M.
	p, ok := pm.Find("claude-sonnet-4-5")
	if !ok {
		t.Fatal("claude-sonnet-4-5 missing")
	}
	if math.Abs(p.Input-3.0/1e6) > 1e-15 || math.Abs(p.Output-15.0/1e6) > 1e-15 {
		t.Errorf("claude-sonnet-4-5 pricing changed: %+v", p)
	}
}
