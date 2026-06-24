package usage

import "github.com/grovetools/agentlogs/pkg/transcript"

// CostMode selects how a transcript entry's cost is determined.
type CostMode int

const (
	// CostModeCalculate always computes cost from token counts and the pricing
	// table. Claude transcripts carry no precomputed cost, so this is the only
	// correct mode for them and the aglogs default.
	CostModeCalculate CostMode = iota
	// CostModeAuto uses a precomputed cost when present, else calculates. Kept
	// for the future gemini summarizer (its query log records EstimatedCost).
	CostModeAuto
	// CostModeDisplay uses only the precomputed cost (0 when absent).
	CostModeDisplay
)

// rawCost is the token-derived cost for a single entry's usage under the given
// pricing, applying the 5m/1h cache-creation split and per-class 200k tiering.
// It mirrors ccusage calculate_cost_from_tokens (speed/fast multiplier is not
// modeled: grove transcripts never carry a fast-speed marker).
func rawCost(u transcript.Usage, pricing Pricing) float64 {
	cache5m := u.CacheCreationInputTokens
	cache1h := 0
	if u.CacheCreation != nil {
		cache5m = u.CacheCreation.Ephemeral5mInputTokens
		cache1h = u.CacheCreation.Ephemeral1hInputTokens
	}

	cache1hRate := pricing.Input * cacheCreate1hInputMultiplier
	var cache1hAbove *float64
	if pricing.InputAbove200k != nil {
		v := *pricing.InputAbove200k * cacheCreate1hInputMultiplier
		cache1hAbove = &v
	}

	return tieredCost(int64(u.InputTokens), pricing.Input, pricing.InputAbove200k) +
		tieredCost(int64(u.OutputTokens), pricing.Output, pricing.OutputAbove200k) +
		tieredCost(int64(cache5m), pricing.CacheCreate, pricing.CacheCreateAbv) +
		tieredCost(int64(cache1h), cache1hRate, cache1hAbove) +
		tieredCost(int64(u.CacheReadInputTokens), pricing.CacheRead, pricing.CacheReadAbove)
}

// EntryCost computes the USD cost for one entry's usage given a model, cost
// mode, precomputed cost (costUSD; nil when none), and pricing table. The
// second return reports the resolved model name when pricing was required but
// missing, so callers can surface an "unpriced" flag rather than a silent $0.
func EntryCost(model string, u transcript.Usage, costUSD *float64, mode CostMode, pm *PricingMap) (float64, string) {
	switch mode {
	case CostModeDisplay:
		if costUSD != nil {
			return *costUSD, ""
		}
		return 0, ""
	case CostModeAuto:
		if costUSD != nil {
			return *costUSD, ""
		}
	}
	// Calculate (or Auto with no precomputed cost).
	if model == "" {
		return 0, ""
	}
	pricing, ok := pm.Find(model)
	if !ok {
		// Only flag as missing when there were tokens to price.
		if usageTokenTotal(u) > 0 {
			return 0, model
		}
		return 0, ""
	}
	return rawCost(u, pricing), ""
}
