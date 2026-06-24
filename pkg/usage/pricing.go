package usage

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// tieringThreshold is the per-token-class boundary above which an "above 200k"
// rate applies (when the model defines one). Ported from ccusage cost.rs.
const tieringThreshold = 200_000

// cacheCreate1hInputMultiplier is the factor applied to the input rate to price
// 1-hour cache-creation tokens (ccusage CACHE_CREATE_1H_INPUT_MULTIPLIER).
const cacheCreate1hInputMultiplier = 2.0

// modelDateSuffixDigits is the digit count of an Anthropic date-suffixed model
// alias (YYYYMMDD); such a suffix is treated as the same model for pricing,
// while other numeric suffixes denote distinct versions. Mirrors ccusage.
const modelDateSuffixDigits = 8

// Pricing holds the per-token USD rates for a single model. Rates are stored
// per token (the embedded models.dev table publishes per-million figures, which
// are divided down at load time). The *Above200k fields, when non-nil, switch
// in for tokens beyond tieringThreshold within that class.
type Pricing struct {
	Input             float64
	Output            float64
	CacheCreate       float64
	CacheRead         float64
	CacheReadExplicit bool
	InputAbove200k    *float64
	OutputAbove200k   *float64
	CacheCreateAbv    *float64
	CacheReadAbove    *float64
}

//go:embed models-dev-pricing.json
var modelsDevPricingJSON []byte

// PricingMap resolves model names to Pricing. It is a thin port of ccusage's
// embedded models.dev fallback table plus its fuzzy key matching, which is the
// pricing source for the Anthropic models grove emits (LiteLLM frequently lags
// new releases, so ccusage itself falls through to this table for them).
type PricingMap struct {
	entries map[string]Pricing
}

// modelsDevEntry mirrors one record in models-dev-pricing.json.
type modelsDevEntry struct {
	ID   string `json:"id"`
	Cost *struct {
		Input     *float64 `json:"input"`
		Output    *float64 `json:"output"`
		CacheRead *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
	} `json:"cost"`
}

// DefaultPricing returns the pricing table built from the embedded models.dev
// snapshot. It never fetches from the network — the embedded table is the
// single source of truth so runs are deterministic and offline-safe.
func DefaultPricing() *PricingMap {
	pm := &PricingMap{entries: make(map[string]Pricing)}
	pm.loadModelsDevJSON(modelsDevPricingJSON)
	return pm
}

// loadModelsDevJSON parses the flat models.dev "Models" format (key -> {cost})
// and inserts per-token Pricing, converting the per-million figures down and
// applying ccusage's cache fallbacks (cache_write defaults to input*1.25,
// cache_read to input*0.1).
func (pm *PricingMap) loadModelsDevJSON(data []byte) {
	var raw map[string]modelsDevEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	for key, entry := range raw {
		modelID := key
		if entry.ID != "" {
			modelID = entry.ID
		}
		if _, exists := pm.entries[modelID]; exists {
			continue
		}
		if entry.Cost == nil || entry.Cost.Input == nil || entry.Cost.Output == nil {
			continue
		}
		input := *entry.Cost.Input / 1_000_000.0
		output := *entry.Cost.Output / 1_000_000.0
		cacheReadExplicit := entry.Cost.CacheRead != nil
		cacheCreate := input * 1.25
		if entry.Cost.CacheWrite != nil {
			cacheCreate = *entry.Cost.CacheWrite / 1_000_000.0
		}
		cacheRead := input * 0.1
		if entry.Cost.CacheRead != nil {
			cacheRead = *entry.Cost.CacheRead / 1_000_000.0
		}
		pm.entries[modelID] = Pricing{
			Input:             input,
			Output:            output,
			CacheCreate:       cacheCreate,
			CacheRead:         cacheRead,
			CacheReadExplicit: cacheReadExplicit,
		}
	}
}

// Find resolves a model name to its Pricing, returning false when no entry
// matches. It tries an exact lookup, then the fuzzy key match (normalizing
// '.'/'@' to '-' and allowing date-suffix / provider-prefix boundaries), the
// same resolution order ccusage uses for its embedded table.
func (pm *PricingMap) Find(model string) (Pricing, bool) {
	if p, ok := pm.entries[model]; ok {
		return p, true
	}
	normalizedModel := normalizedPricingKey(model)
	var best string
	var bestPricing Pricing
	found := false
	for candidate, pricing := range pm.entries {
		if !pricingKeyMatches(candidate, model, normalizedModel) {
			continue
		}
		// Prefer the longest candidate; ties broken by reverse-lexical order
		// to match ccusage's max_by(len, then right.cmp(left)).
		if !found || len(candidate) > len(best) || (len(candidate) == len(best) && candidate < best) {
			best = candidate
			bestPricing = pricing
			found = true
		}
	}
	return bestPricing, found
}

// normalizedPricingKey replaces the '.'/'@' separator variants with '-' so that
// e.g. "claude-opus-4.6" and "claude-opus-4-6" compare equal.
func normalizedPricingKey(value string) string {
	if strings.ContainsAny(value, ".@") {
		return strings.NewReplacer(".", "-", "@", "-").Replace(value)
	}
	return value
}

// pricingKeyMatches reports whether candidate and model name the same model,
// directly or after separator normalization, honoring version boundaries.
func pricingKeyMatches(candidate, model, normalizedModel string) bool {
	if containsPricingKey(model, candidate) || containsPricingKey(candidate, model) {
		return true
	}
	normalizedCandidate := normalizedPricingKey(candidate)
	return containsPricingKey(normalizedModel, normalizedCandidate) ||
		containsPricingKey(normalizedCandidate, normalizedModel)
}

// containsPricingKey finds key inside value only when bounded by non-alphanumeric
// edges, and not when the trailing separator introduces a distinct numeric
// version (other than an 8-digit date suffix). Port of ccusage contains_pricing_key.
func containsPricingKey(value, key string) bool {
	if key == "" {
		return false
	}
	from := 0
	for {
		idx := strings.Index(value[from:], key)
		if idx < 0 {
			return false
		}
		idx += from
		beforeOK := idx == 0 || isPricingKeyBoundary(value[idx-1])
		suffix := value[idx+len(key):]
		if beforeOK && suffixAllowsPricingKeyMatch(key, suffix) {
			return true
		}
		from = idx + 1
		if from >= len(value) {
			return false
		}
	}
}

func isPricingKeyBoundary(b byte) bool {
	return !isASCIIAlphanumeric(b)
}

func isASCIIAlphanumeric(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func suffixAllowsPricingKeyMatch(key, suffix string) bool {
	if suffix == "" {
		return true
	}
	sep := suffix[0]
	if !isPricingKeyBoundary(sep) {
		return false
	}
	return !suffixStartsWithNumericModelVersion(key, suffix)
}

// suffixStartsWithNumericModelVersion reports that suffix begins a numeric model
// version (so the match should be rejected), unless that run is exactly an
// 8-digit date suffix (which is the same model).
func suffixStartsWithNumericModelVersion(key, suffix string) bool {
	if len(key) == 0 || !isASCIIDigit(key[len(key)-1]) {
		return false
	}
	if suffix[0] != '-' && suffix[0] != '.' {
		return false
	}
	rest := suffix[1:]
	digitLen := 0
	for digitLen < len(rest) && isASCIIDigit(rest[digitLen]) {
		digitLen++
	}
	if digitLen == 0 {
		return false
	}
	afterIsBoundary := true
	if digitLen < len(rest) {
		afterIsBoundary = isPricingKeyBoundary(rest[digitLen])
	}
	return !(digitLen == modelDateSuffixDigits && afterIsBoundary)
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// tieredCost prices tokens of one class, applying the above-threshold rate to
// the portion over tieringThreshold when one is defined. Port of ccusage tiered_cost.
func tieredCost(tokens int64, base float64, above *float64) float64 {
	if tokens == 0 {
		return 0
	}
	if above != nil && tokens > tieringThreshold {
		return float64(tieringThreshold)*base + float64(tokens-tieringThreshold)*(*above)
	}
	return float64(tokens) * base
}
