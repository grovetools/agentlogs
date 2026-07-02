package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	"github.com/grovetools/agentlogs/pkg/usage"
)

// ccusageModelBreakdown mirrors one entry of ccusage's session modelBreakdowns
// array (camelCase, per-million cost in USD).
type ccusageModelBreakdown struct {
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	Cost                float64 `json:"cost"`
	InputTokens         int64   `json:"inputTokens"`
	ModelName           string  `json:"modelName"`
	OutputTokens        int64   `json:"outputTokens"`
}

// ccusageSession mirrors one ccusage `claude session --json` session object.
type ccusageSession struct {
	CacheCreationTokens int64                   `json:"cacheCreationTokens"`
	CacheReadTokens     int64                   `json:"cacheReadTokens"`
	FirstActivity       string                  `json:"firstActivity"`
	InputTokens         int64                   `json:"inputTokens"`
	LastActivity        string                  `json:"lastActivity"`
	ModelBreakdowns     []ccusageModelBreakdown `json:"modelBreakdowns"`
	ModelsUsed          []string                `json:"modelsUsed"`
	OutputTokens        int64                   `json:"outputTokens"`
	ProjectPath         string                  `json:"projectPath"`
	SessionID           string                  `json:"sessionId"`
	TotalCost           float64                 `json:"totalCost"`
	TotalTokens         int64                   `json:"totalTokens"`
}

// ccusageTotals mirrors the ccusage session report totals object.
type ccusageTotals struct {
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	InputTokens         int64   `json:"inputTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	TotalCost           float64 `json:"totalCost"`
	TotalTokens         int64   `json:"totalTokens"`
}

// ccusageReport is the full ccusage `claude session --json` document shape. The
// usage command emits this under --ccusage-json so the acceptance gate can diff
// it directly against the ccusage binary.
type ccusageReport struct {
	Sessions []ccusageSession `json:"sessions"`
	Totals   ccusageTotals    `json:"totals"`
}

func newUsageCmd() *cobra.Command {
	var (
		jsonOutput  bool
		ccusageJSON bool
		sessionID   string
		sinceDur    string
		blocks      bool
		watch       bool
		blockHours  float64
		watchEvery  string
		limit       int64
		providerCSV string
	)

	cmd := cli.NewStandardCommand("usage", "Show token usage and cost across sessions")
	cmd.Use = "usage [flags]"
	cmd.Long = `Aggregates token usage and cost across coding-agent sessions.

By default scans every provider's local session store — Claude
(~/.claude/projects), codex (~/.codex/sessions), pi (~/.pi/agent/sessions),
and opencode (storage, via the fragment assembler) — dedups duplicate API
responses (by message id + request id, with sidechain-replay fallback), and
reports per-session and grand totals. Cost is the provider-native figure when
one is recorded (pi per-message cost; opencode's cost field) and otherwise
computed with the cache-aware 4-class pricer. --provider narrows the scan
(e.g. --provider claude, or a comma list).

Use --session <id> to roll up a single job (for Claude: parent transcript
plus its ad-hoc Task subagents and workflow agents, matched by inner session
id; other providers roll up the matching session's entries).

Use --blocks to group usage into rolling 5-hour blocks with burn rate and a
linear projection for the active block. Add --watch to refresh that block view
live. --limit <tokens> sets a config-defined denominator (there is no live
limits API) so the projection shows a percent-of-limit and OK/WARNING/EXCEEDS.

--ccusage-json emits the exact ccusage 'claude session --json' document shape
(path-derived session grouping) for the acceptance gate; it always scans
Claude only, regardless of --provider.`
	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		providers, err := parseProviderFlag(providerCSV)
		if err != nil {
			return err
		}
		claudeOnly := len(providers) == 1 && providers[0] == "claude"

		duration := usage.DefaultSessionBlockDuration
		if blockHours > 0 {
			duration = time.Duration(blockHours * float64(time.Hour))
		}

		// Live block watcher: tail parent + open agent files, redraw on a timer.
		if watch {
			every := 2 * time.Second
			if watchEvery != "" {
				d, err := time.ParseDuration(watchEvery)
				if err != nil {
					return fmt.Errorf("invalid --watch-interval duration %q: %w", watchEvery, err)
				}
				every = d
			}
			return runUsageWatch(cmd.Context(), sessionID, duration, limit, every, providers)
		}

		// Block view (5-hour windows + burn rate + projection).
		if blocks {
			reports, err := usageBlockReports(sessionID, duration, providers)
			if err != nil {
				return fmt.Errorf("could not compute usage blocks: %w", err)
			}
			if jsonOutput {
				return printJSON(reports)
			}
			printBlocks(os.Stdout, reports, limit, claudeOnly)
			return nil
		}

		var since time.Time
		if sinceDur != "" {
			d, err := time.ParseDuration(sinceDur)
			if err != nil {
				return fmt.Errorf("invalid --since duration %q: %w", sinceDur, err)
			}
			since = time.Now().Add(-d)
		}

		// Single-session rollup (product view): parent + ad-hoc + workflow.
		if sessionID != "" {
			var s usage.Summary
			var err error
			if claudeOnly {
				// The historical Claude-only path, unchanged.
				s, err = usage.SummarizeSession(nil, sessionID, usage.CostModeCalculate)
			} else {
				// Claude discovery first (same code path), non-claude sources
				// only when it finds nothing.
				s, err = usage.SummarizeSessionAcrossProviders(nil, sessionID, usage.CostModeCalculate)
			}
			if err != nil {
				return fmt.Errorf("could not summarize session %q: %w", sessionID, err)
			}
			if jsonOutput || ccusageJSON {
				return printJSON(s)
			}
			printSummaryText(s)
			return nil
		}

		// The ccusage acceptance gate diffs against `ccusage claude session`;
		// it is Claude-only by definition.
		if ccusageJSON {
			result, err := usage.ScanProjects(nil, usage.CostModeCalculate, since)
			if err != nil {
				return fmt.Errorf("could not scan projects: %w", err)
			}
			return printJSON(toCcusageReport(result))
		}

		var result usage.ScanResult
		if claudeOnly {
			// The historical Claude-only scan, unchanged.
			result, err = usage.ScanProjects(nil, usage.CostModeCalculate, since)
		} else {
			result, err = usage.ScanUsage(providers, usage.CostModeCalculate, since)
		}
		if err != nil {
			return fmt.Errorf("could not scan sessions: %w", err)
		}

		if jsonOutput {
			return printJSON(result)
		}
		printScanText(result)
		return nil
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&ccusageJSON, "ccusage-json", false, "Output the ccusage 'claude session --json' document shape (Claude only)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Roll up a single session (parent + subagents + workflow)")
	cmd.Flags().StringVar(&sinceDur, "since", "", "Only count entries newer than this duration (e.g. 24h, 168h)")
	cmd.Flags().BoolVar(&blocks, "blocks", false, "Group usage into rolling 5-hour blocks with burn rate and projection")
	cmd.Flags().BoolVar(&watch, "watch", false, "Live-tail the active block (burn rate, projection); refreshes on a timer")
	cmd.Flags().Float64Var(&blockHours, "block-hours", 0, "Rolling block window in hours (default 5)")
	cmd.Flags().StringVar(&watchEvery, "watch-interval", "", "Refresh interval for --watch (default 2s)")
	cmd.Flags().Int64Var(&limit, "limit", 0, "Config-defined token denominator for the block projection (no live limits API)")
	cmd.Flags().StringVar(&providerCSV, "provider", "all", "Providers to scan: all, or a comma list of claude,codex,opencode,pi")

	return cmd
}

// parseProviderFlag expands the --provider value to the provider list:
// "all" (or empty) means every provider with a usage source.
func parseProviderFlag(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return usage.AllProviders, nil
	}
	known := make(map[string]bool, len(usage.AllProviders))
	for _, p := range usage.AllProviders {
		known[p] = true
	}
	var providers []string
	for _, p := range strings.Split(value, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !known[p] {
			return nil, fmt.Errorf("unknown provider %q (known: %s, or all)", p, strings.Join(usage.AllProviders, ", "))
		}
		providers = append(providers, p)
	}
	if len(providers) == 0 {
		return usage.AllProviders, nil
	}
	return providers, nil
}

// usageBlockReports computes block reports for the given scope: a single
// session (Claude rollup first, matching the historical --session semantics)
// or the union of the given providers' stores. Claude-only provider lists go
// through the historical ProjectBlocks path unchanged.
func usageBlockReports(sessionID string, duration time.Duration, providers []string) ([]usage.BlockReport, error) {
	if sessionID != "" {
		return usage.SessionBlocks(nil, sessionID, usage.CostModeCalculate, duration)
	}
	if len(providers) == 1 && providers[0] == "claude" {
		return usage.ProjectBlocks(nil, usage.CostModeCalculate, duration)
	}
	return usage.ProviderBlocks(providers, usage.CostModeCalculate, duration)
}

// toCcusageReport converts a ScanResult into the ccusage session-report shape.
func toCcusageReport(r usage.ScanResult) ccusageReport {
	report := ccusageReport{}
	for _, s := range r.Sessions {
		report.Sessions = append(report.Sessions, toCcusageSession(s))
	}
	report.Totals = ccusageTotals{
		CacheCreationTokens: r.Totals.Usage.CacheWrite5m + r.Totals.Usage.CacheWrite1h,
		CacheReadTokens:     r.Totals.Usage.CacheRead,
		InputTokens:         r.Totals.Usage.Input,
		OutputTokens:        r.Totals.Usage.Output,
		TotalCost:           r.Totals.CostUSD,
		TotalTokens:         r.Totals.Usage.Total(),
	}
	return report
}

func toCcusageSession(s usage.Summary) ccusageSession {
	cs := ccusageSession{
		CacheCreationTokens: s.Usage.CacheWrite5m + s.Usage.CacheWrite1h,
		CacheReadTokens:     s.Usage.CacheRead,
		FirstActivity:       formatActivity(s.FirstActivity),
		InputTokens:         s.Usage.Input,
		LastActivity:        formatActivity(s.LastActivity),
		ModelsUsed:          s.Models,
		OutputTokens:        s.Usage.Output,
		ProjectPath:         s.ProjectPath,
		SessionID:           s.SessionID,
		TotalCost:           s.CostUSD,
		TotalTokens:         s.Usage.Total(),
	}
	if cs.ModelsUsed == nil {
		cs.ModelsUsed = []string{}
	}
	for _, mb := range s.ModelBreakdown {
		cs.ModelBreakdowns = append(cs.ModelBreakdowns, ccusageModelBreakdown{
			CacheCreationTokens: mb.Usage.CacheWrite5m + mb.Usage.CacheWrite1h,
			CacheReadTokens:     mb.Usage.CacheRead,
			Cost:                mb.CostUSD,
			InputTokens:         mb.Usage.Input,
			ModelName:           mb.Model,
			OutputTokens:        mb.Usage.Output,
		})
	}
	if cs.ModelBreakdowns == nil {
		cs.ModelBreakdowns = []ccusageModelBreakdown{}
	}
	// ccusage sorts modelBreakdowns by modelName for stable output.
	sort.Slice(cs.ModelBreakdowns, func(i, j int) bool {
		return cs.ModelBreakdowns[i].ModelName < cs.ModelBreakdowns[j].ModelName
	})
	return cs
}

// formatActivity renders a timestamp like ccusage (RFC3339 milliseconds, Z).
func formatActivity(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printSummaryText(s usage.Summary) {
	fmt.Printf("Session: %s\n", s.SessionID)
	if s.ProjectPath != "" {
		fmt.Printf("Project: %s\n", s.ProjectPath)
	}
	fmt.Printf("Messages: %d\n", s.MessageCount)
	fmt.Printf("Input:           %d\n", s.Usage.Input)
	fmt.Printf("Output:          %d\n", s.Usage.Output)
	fmt.Printf("Cache creation:  %d\n", s.Usage.CacheWrite5m+s.Usage.CacheWrite1h)
	fmt.Printf("Cache read:      %d\n", s.Usage.CacheRead)
	fmt.Printf("Total tokens:    %d\n", s.Usage.Total())
	fmt.Printf("Context size:    %d\n", s.ContextSize)
	fmt.Printf("Cost (USD):      $%.4f\n", s.CostUSD)
	if s.MissingPricing {
		fmt.Println("(warning: some models had no pricing; cost is a lower bound)")
	}
	if len(s.Agents) > 0 {
		fmt.Println("\nPer-agent:")
		for _, a := range s.Agents {
			fmt.Printf("  %-16s tokens=%d cost=$%.4f\n", a.AgentID, a.Usage.Total(), a.CostUSD)
		}
	}
}

func printScanText(r usage.ScanResult) {
	fmt.Printf("Sessions: %d\n", len(r.Sessions))
	fmt.Printf("Total input:          %d\n", r.Totals.Usage.Input)
	fmt.Printf("Total output:         %d\n", r.Totals.Usage.Output)
	fmt.Printf("Total cache creation: %d\n", r.Totals.Usage.CacheWrite5m+r.Totals.Usage.CacheWrite1h)
	fmt.Printf("Total cache read:     %d\n", r.Totals.Usage.CacheRead)
	fmt.Printf("Total tokens:         %d\n", r.Totals.Usage.Total())
	fmt.Printf("Total cost (USD):     $%.4f\n", r.Totals.CostUSD)
	if r.Totals.MissingPricing {
		fmt.Println("(warning: some models had no pricing; cost is a lower bound)")
	}
}
