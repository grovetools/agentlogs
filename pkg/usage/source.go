package usage

import (
	"fmt"
	"sort"
	"time"
)

// usageSource loads usage-bearing entries from one provider's local session
// store. Sources are collection-only: the shared pipeline (since filter,
// dedup, grouping, pricing, block identification) is provider-neutral and
// lives in scanResultFromEntries / blockEntriesFromLoaded, so a new provider
// is one source implementation, not another aggregation walker.
type usageSource interface {
	// Provider returns the provider name the source scans ("claude", ...).
	Provider() string
	// CollectEntries loads every usage-bearing entry across the provider's
	// local sessions. A missing store (provider not installed / never used)
	// returns (nil, nil) — it is not an error for a provider to be absent.
	CollectEntries() ([]loadedEntry, error)
}

// AllProviders lists every provider with a usage source, in scan order.
// "claude" is first: its entries dominate in practice and the grand total is
// order-independent either way.
var AllProviders = []string{"claude", "codex", "opencode", "pi"}

// sourceForProvider returns the usage source for a provider name.
func sourceForProvider(provider string) (usageSource, error) {
	switch provider {
	case "claude":
		return claudeUsageSource{}, nil
	case "codex":
		return codexUsageSource{}, nil
	case "opencode":
		return opencodeUsageSource{}, nil
	case "pi":
		return piUsageSource{}, nil
	default:
		return nil, fmt.Errorf("no usage source for provider %q (known: claude, codex, opencode, pi)", provider)
	}
}

// claudeUsageSource wraps the historical ~/.claude/projects walker.
type claudeUsageSource struct{}

func (claudeUsageSource) Provider() string { return "claude" }

// CollectEntries loads every Claude entry via the exact walker ScanProjects
// uses. Entries keep their zero-value Provider ("" ≡ claude) so Claude
// summaries serialize byte-identically to the pre-provider-routing output.
func (claudeUsageSource) CollectEntries() ([]loadedEntry, error) {
	return collectClaudeEntries(nil)
}

// collectProviderEntries unions the entries of the given providers' sources.
// A provider whose store is absent contributes nothing; a provider with no
// source is an error (caller typo).
func collectProviderEntries(providers []string) ([]loadedEntry, error) {
	if len(providers) == 0 {
		providers = AllProviders
	}
	var all []loadedEntry
	for _, p := range providers {
		src, err := sourceForProvider(p)
		if err != nil {
			return nil, err
		}
		entries, err := src.CollectEntries()
		if err != nil {
			return nil, fmt.Errorf("collecting %s usage: %w", p, err)
		}
		all = append(all, entries...)
	}
	return all, nil
}

// ScanUsage is the multi-provider analogue of ScanProjects: it unions every
// listed provider's usage source (nil/empty = AllProviders), dedups, groups
// per (provider, project, session), prices, and returns per-session summaries
// plus a grand total. ScanUsage([]string{"claude"}, ...) is exactly
// ScanProjects(nil, ...).
func ScanUsage(providers []string, mode CostMode, since time.Time) (ScanResult, error) {
	all, err := collectProviderEntries(providers)
	if err != nil {
		return ScanResult{}, err
	}
	return scanResultFromEntries(all, mode, since), nil
}

// ProviderBlocks is the multi-provider analogue of ProjectBlocks: rolling
// usage blocks (burn rate + projection on the active block) over the union of
// the listed providers' usage sources (nil/empty = AllProviders).
func ProviderBlocks(providers []string, mode CostMode, duration time.Duration) ([]BlockReport, error) {
	all, err := collectProviderEntries(providers)
	if err != nil {
		return nil, err
	}
	pm := DefaultPricing()
	entries := blockEntriesFromLoaded(all, mode, pm)
	return BuildBlockReports(entries, duration, time.Now()), nil
}

// SummarizeSessionAcrossProviders resolves a --session rollup across every
// provider: the Claude path (parent + ad-hoc + workflow subagent discovery)
// runs first and unchanged; only when it finds no billable usage are the
// non-Claude sources consulted for entries whose session id matches.
func SummarizeSessionAcrossProviders(slugDirs []string, sessionID string, mode CostMode) (Summary, error) {
	s, err := SummarizeSession(slugDirs, sessionID, mode)
	if err == nil && s.Usage.Total() > 0 {
		return s, nil
	}
	claudeErr := err

	for _, p := range AllProviders {
		if p == "claude" {
			continue
		}
		src, srcErr := sourceForProvider(p)
		if srcErr != nil {
			continue
		}
		entries, collectErr := src.CollectEntries()
		if collectErr != nil {
			continue
		}
		var matched []loadedEntry
		for _, e := range entries {
			if e.SessionID == sessionID {
				matched = append(matched, e)
			}
		}
		if len(matched) == 0 {
			continue
		}
		sort.Slice(matched, func(i, j int) bool {
			return matched[i].Timestamp.Before(matched[j].Timestamp)
		})
		matched = dedupe(matched)
		pm := DefaultPricing()
		return summarize(sessionID, matched[0].ProjectPath, matched, nil, mode, pm), nil
	}

	if claudeErr != nil {
		return Summary{}, claudeErr
	}
	return s, nil
}
