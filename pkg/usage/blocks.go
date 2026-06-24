package usage

import (
	"sort"
	"time"
)

// DefaultSessionBlockDuration is the rolling window length for usage blocks.
// Claude Code bills against a 5-hour window, so this is the ccusage default
// (session_duration_hours = 5).
const DefaultSessionBlockDuration = 5 * time.Hour

// blocksWarningThreshold is the fraction of a configured limit denominator at
// which a projection is flagged "warning" rather than "ok" (ccusage
// BLOCKS_WARNING_THRESHOLD).
const blocksWarningThreshold = 0.8

// blockEntry is the minimal per-message record the block algorithm needs: a
// timestamp, its typed usage, the resolved model, and the already-priced cost.
// It is the Go analogue of ccusage's LoadedEntry as consumed by blocks.rs.
type blockEntry struct {
	Timestamp time.Time
	Usage     Usage
	Model     string
	CostUSD   float64
}

// Block is one rolling usage window (ccusage SessionBlock). A block starts at
// the floored-to-hour timestamp of its first entry and nominally spans
// Duration; a new block opens when an entry is more than Duration past the
// block start or more than Duration after the previous entry. Gap blocks (no
// entries) mark idle stretches longer than Duration.
type Block struct {
	ID            string    `json:"id"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	ActualEndTime time.Time `json:"actual_end_time,omitempty"`
	IsActive      bool      `json:"is_active"`
	IsGap         bool      `json:"is_gap"`
	EntryCount    int       `json:"entry_count"`
	Usage         Usage     `json:"usage"`
	CostUSD       float64   `json:"cost_usd"`
	Models        []string  `json:"models"`
}

// TotalTokens is the block's summed token count across all classes.
func (b Block) TotalTokens() int64 { return b.Usage.Total() }

// BurnRate is the consumption velocity of an active block (ccusage BurnRate).
// TokensPerMinute counts every class; TokensPerMinuteForIndicator counts only
// non-cache (input+output) tokens, which is the figure ccusage uses for its
// "normal/moderate/high" indicator since cache reads dominate raw throughput.
type BurnRate struct {
	TokensPerMinute             float64 `json:"tokens_per_minute"`
	TokensPerMinuteForIndicator float64 `json:"tokens_per_minute_for_indicator"`
	CostPerHour                 float64 `json:"cost_per_hour"`
}

// Projection linearly extrapolates an active block's burn rate to the block's
// end time (ccusage Projection).
type Projection struct {
	TotalTokens      int64   `json:"total_tokens"`
	TotalCost        float64 `json:"total_cost"`
	RemainingMinutes int64   `json:"remaining_minutes"`
}

// IdentifySessionBlocks groups timestamp-ordered entries into rolling blocks of
// the given duration, floored to the hour, inserting gap blocks for idle
// stretches longer than duration. now sets the reference for active-block
// detection (pass time.Now() in production; a fixed time in tests). It is a
// direct port of ccusage identify_session_blocks.
func IdentifySessionBlocks(entries []blockEntry, duration time.Duration, now time.Time) []Block {
	if len(entries) == 0 {
		return nil
	}
	sorted := make([]blockEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	var blocks []Block
	var currentStart time.Time
	haveStart := false
	var current []blockEntry

	for _, e := range sorted {
		if haveStart {
			lastTime := currentStart
			if n := len(current); n > 0 {
				lastTime = current[n-1].Timestamp
			}
			sinceStart := e.Timestamp.Sub(currentStart)
			sinceLast := e.Timestamp.Sub(lastTime)
			if sinceStart > duration || sinceLast > duration {
				blocks = append(blocks, createBlock(currentStart, current, now, duration))
				if sinceLast > duration {
					blocks = append(blocks, createGapBlock(lastTime, e.Timestamp, duration))
				}
				current = nil
				currentStart = floorToHour(e.Timestamp)
			}
		} else {
			currentStart = floorToHour(e.Timestamp)
			haveStart = true
		}
		current = append(current, e)
	}

	if haveStart && len(current) > 0 {
		blocks = append(blocks, createBlock(currentStart, current, now, duration))
	}
	return blocks
}

// floorToHour truncates a timestamp down to the start of its UTC hour, matching
// ccusage TimestampMs::floor_to_hour (floor of epoch-millis to the hour).
func floorToHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// createBlock builds a populated (non-gap) block from its entries, summing
// usage/cost and collecting the distinct models in first-seen order. A block is
// active when its last entry is younger than duration and now is before the
// block end (ccusage create_block).
func createBlock(start time.Time, entries []blockEntry, now time.Time, duration time.Duration) Block {
	end := start.Add(duration)
	var actualEnd time.Time
	if n := len(entries); n > 0 {
		actualEnd = entries[n-1].Timestamp
	}
	isActive := !actualEnd.IsZero() && now.Sub(actualEnd) < duration && now.Before(end)

	b := Block{
		ID:            start.UTC().Format(time.RFC3339),
		StartTime:     start,
		EndTime:       end,
		ActualEndTime: actualEnd,
		IsActive:      isActive,
		EntryCount:    len(entries),
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		b.Usage.Add(e.Usage)
		b.CostUSD += e.CostUSD
		if e.Model != "" && !seen[e.Model] {
			seen[e.Model] = true
			b.Models = append(b.Models, e.Model)
		}
	}
	return b
}

// createGapBlock builds an empty gap block spanning the idle stretch between
// the previous block's reach (last entry + duration) and the next entry
// (ccusage create_gap_block).
func createGapBlock(last, next time.Time, duration time.Duration) Block {
	start := last.Add(duration)
	return Block{
		ID:        "gap-" + start.UTC().Format(time.RFC3339),
		StartTime: start,
		EndTime:   next,
		IsGap:     true,
	}
}

// CalculateBurnRate computes the consumption velocity of a block from the span
// between its first and last entry. Returns nil for gap/empty blocks or when
// the span is non-positive (ccusage calculate_burn_rate). The entries slice
// must be the same one used to build the block (sorted ascending).
func CalculateBurnRate(b Block, entries []blockEntry) *BurnRate {
	if b.IsGap || len(entries) == 0 {
		return nil
	}
	first := entries[0].Timestamp
	last := entries[len(entries)-1].Timestamp
	durationMinutes := last.Sub(first).Minutes()
	if durationMinutes <= 0 {
		return nil
	}
	total := float64(b.Usage.Total())
	nonCache := float64(b.Usage.Input + b.Usage.Output)
	return &BurnRate{
		TokensPerMinute:             total / durationMinutes,
		TokensPerMinuteForIndicator: nonCache / durationMinutes,
		CostPerHour:                 b.CostUSD / durationMinutes * 60.0,
	}
}

// ProjectBlockUsage extrapolates an active block's burn rate to its end time,
// returning the projected total tokens/cost and the remaining minutes. Returns
// nil for inactive/gap blocks or when no burn rate is available (ccusage
// project_block_usage).
func ProjectBlockUsage(b Block, entries []blockEntry, now time.Time) *Projection {
	if !b.IsActive || b.IsGap {
		return nil
	}
	burn := CalculateBurnRate(b, entries)
	if burn == nil {
		return nil
	}
	remainingMinutes := roundFloat(b.EndTime.Sub(now).Minutes())
	totalTokens := float64(b.Usage.Total()) + burn.TokensPerMinute*remainingMinutes
	totalCost := b.CostUSD + (burn.CostPerHour/60.0)*remainingMinutes
	return &Projection{
		TotalTokens:      int64(roundFloat(totalTokens)),
		TotalCost:        roundFloat(totalCost*100.0) / 100.0,
		RemainingMinutes: int64(remainingMinutes),
	}
}

// roundFloat rounds to the nearest integer (half away from zero), matching
// Rust f64::round used throughout the projection math.
func roundFloat(f float64) float64 {
	if f < 0 {
		return float64(int64(f - 0.5))
	}
	return float64(int64(f + 0.5))
}

// LimitState classifies a projection or current usage against a configured
// denominator (a config-defined limit; there is no live limits API).
type LimitState string

const (
	LimitOK       LimitState = "ok"
	LimitWarning  LimitState = "warning"
	LimitExceeded LimitState = "exceeds"
)

// LimitStatus reports usage against a config-defined denominator. The limit is
// a denominator only (e.g. a session/weekly token budget from config); grove
// has no limits API, so this never claims a real remaining-quota figure — it is
// purely arithmetic over the configured number.
type LimitStatus struct {
	Limit          int64      `json:"limit"`
	Used           int64      `json:"used"`
	ProjectedUsage int64      `json:"projected_usage,omitempty"`
	PercentUsed    float64    `json:"percent_used"`
	State          LimitState `json:"state"`
}

// EvaluateLimit computes a LimitStatus for a current/projected token count
// against a configured denominator. When projected is non-zero it drives the
// state classification (matching ccusage's token-limit projection logic);
// otherwise current is used. A non-positive limit yields a zeroed status with
// state "ok" (no denominator configured).
func EvaluateLimit(current, projected, limit int64) LimitStatus {
	st := LimitStatus{Limit: limit, Used: current, ProjectedUsage: projected}
	if limit <= 0 {
		st.State = LimitOK
		return st
	}
	measure := current
	if projected > 0 {
		measure = projected
	}
	st.PercentUsed = float64(measure) / float64(limit) * 100.0
	switch {
	case measure > limit:
		st.State = LimitExceeded
	case float64(measure) > float64(limit)*blocksWarningThreshold:
		st.State = LimitWarning
	default:
		st.State = LimitOK
	}
	return st
}

// ActiveBlock returns the single active block from a block list, or nil when
// none is active. The active block doubles as the "stale / will re-cache"
// signal: if no block is active, the last conversation is older than the
// session window and its cache has expired.
func ActiveBlock(blocks []Block) *Block {
	for i := range blocks {
		if blocks[i].IsActive {
			return &blocks[i]
		}
	}
	return nil
}
