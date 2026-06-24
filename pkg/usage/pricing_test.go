package usage

import (
	"math"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

const floatEpsilon = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < floatEpsilon
}

// opusPricing is the resolved Pricing for claude-opus-4.x (per-token), matching
// the embedded models.dev figures: 5/25/6.25/0.5 per million.
func opusPricing(t *testing.T) Pricing {
	t.Helper()
	pm := DefaultPricing()
	p, ok := pm.Find("claude-opus-4-5-20251101")
	if !ok {
		t.Fatalf("expected pricing for claude-opus-4-5-20251101")
	}
	return p
}

func TestFindFuzzyMatch(t *testing.T) {
	pm := DefaultPricing()
	cases := []struct {
		model       string
		wantInputM  float64 // expected input rate per million
		wantFound   bool
	}{
		{"claude-opus-4-5-20251101", 5, true},
		{"claude-opus-4-8", 5, true},
		{"claude-haiku-4-5-20251001", 1, true},
		{"claude-opus-4-1-20250805", 15, true}, // opus 4.1 is the older 15/M tier
		{"definitely-not-a-real-model", 0, false},
	}
	for _, tc := range cases {
		p, ok := pm.Find(tc.model)
		if ok != tc.wantFound {
			t.Errorf("Find(%q) found=%v, want %v", tc.model, ok, tc.wantFound)
			continue
		}
		if !ok {
			continue
		}
		if !almostEqual(p.Input, tc.wantInputM/1_000_000.0) {
			t.Errorf("Find(%q) input=%g, want %g/M", tc.model, p.Input, tc.wantInputM)
		}
	}
}

func TestTieredCost(t *testing.T) {
	above := 2.0
	cases := []struct {
		name   string
		tokens int64
		base   float64
		above  *float64
		want   float64
	}{
		{"zero", 0, 1.0, nil, 0},
		{"flat no tier", 100, 1.0, nil, 100},
		{"under threshold with tier", 100, 1.0, &above, 100},
		{"exactly threshold", tieringThreshold, 1.0, &above, float64(tieringThreshold)},
		{"over threshold splits", tieringThreshold + 10, 1.0, &above, float64(tieringThreshold)*1.0 + 10*2.0},
	}
	for _, tc := range cases {
		got := tieredCost(tc.tokens, tc.base, tc.above)
		if !almostEqual(got, tc.want) {
			t.Errorf("%s: tieredCost=%g, want %g", tc.name, got, tc.want)
		}
	}
}

func TestRawCostFlatCacheCreation(t *testing.T) {
	p := opusPricing(t)
	// Flat cache_creation_input_tokens counts wholly as 5m at the cache-create rate.
	u := transcript.Usage{
		InputTokens:              1000,
		OutputTokens:             2000,
		CacheCreationInputTokens: 4000,
		CacheReadInputTokens:     8000,
	}
	want := 1000*p.Input + 2000*p.Output + 4000*p.CacheCreate + 8000*p.CacheRead
	got := rawCost(u, p)
	if !almostEqual(got, want) {
		t.Errorf("rawCost flat=%g, want %g", got, want)
	}
}

func TestRawCost5mVs1hSplit(t *testing.T) {
	p := opusPricing(t)
	// When the detailed breakdown is present, 1h tokens price at input*2 while
	// 5m tokens price at the cache-create rate. The flat field is ignored.
	u := transcript.Usage{
		InputTokens:              1000,
		OutputTokens:             0,
		CacheCreationInputTokens: 9999, // ignored when CacheCreation is set
		CacheReadInputTokens:     0,
		CacheCreation: &transcript.CacheCreation{
			Ephemeral5mInputTokens: 3000,
			Ephemeral1hInputTokens: 5000,
		},
	}
	cache1hRate := p.Input * cacheCreate1hInputMultiplier
	want := 1000*p.Input + 3000*p.CacheCreate + 5000*cache1hRate
	got := rawCost(u, p)
	if !almostEqual(got, want) {
		t.Errorf("rawCost 5m/1h split=%g, want %g", got, want)
	}
}

func TestEntryCostModes(t *testing.T) {
	pm := DefaultPricing()
	u := transcript.Usage{InputTokens: 1000}
	precomputed := 42.0

	// Display: always the precomputed value (0 when absent).
	if got, _ := EntryCost("claude-opus-4-5", u, &precomputed, CostModeDisplay, pm); !almostEqual(got, 42.0) {
		t.Errorf("Display with precomputed=%g, want 42", got)
	}
	if got, _ := EntryCost("claude-opus-4-5", u, nil, CostModeDisplay, pm); !almostEqual(got, 0) {
		t.Errorf("Display without precomputed=%g, want 0", got)
	}

	// Auto: precomputed when present, else calculated.
	if got, _ := EntryCost("claude-opus-4-5", u, &precomputed, CostModeAuto, pm); !almostEqual(got, 42.0) {
		t.Errorf("Auto with precomputed=%g, want 42", got)
	}
	gotAuto, _ := EntryCost("claude-opus-4-5", u, nil, CostModeAuto, pm)
	gotCalc, _ := EntryCost("claude-opus-4-5", u, nil, CostModeCalculate, pm)
	if !almostEqual(gotAuto, gotCalc) || gotCalc <= 0 {
		t.Errorf("Auto-no-precomputed=%g should equal Calculate=%g (>0)", gotAuto, gotCalc)
	}

	// Calculate with unknown model + tokens flags the missing model.
	if _, missing := EntryCost("totally-unknown-model", u, nil, CostModeCalculate, pm); missing == "" {
		t.Error("Calculate with unknown model and tokens should report missing model")
	}
	// Unknown model with no tokens does not flag.
	if _, missing := EntryCost("totally-unknown-model", transcript.Usage{}, nil, CostModeCalculate, pm); missing != "" {
		t.Errorf("Calculate with no tokens should not flag missing, got %q", missing)
	}
}
