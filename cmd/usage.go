package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
	)

	cmd := cli.NewStandardCommand("usage", "Show token usage and cost across sessions")
	cmd.Use = "usage [flags]"
	cmd.Long = `Aggregates token usage and cost across all Claude sessions.

By default scans every session under ~/.claude/projects, dedups duplicate API
responses (by message id + request id, with sidechain-replay fallback), prices
each entry with a cache-aware 4-class pricer, and reports per-session and grand
totals.

Use --session <id> to roll up a single job (parent transcript plus its ad-hoc
Task subagents and workflow agents, matched by inner session id).

Use --blocks to group usage into rolling 5-hour blocks with burn rate and a
linear projection for the active block. Add --watch to refresh that block view
live. --limit <tokens> sets a config-defined denominator (there is no live
limits API) so the projection shows a percent-of-limit and OK/WARNING/EXCEEDS.

--ccusage-json emits the exact ccusage 'claude session --json' document shape
(path-derived session grouping) for the acceptance gate.`
	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
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
			return runUsageWatch(cmd.Context(), sessionID, duration, limit, every)
		}

		// Block view (5-hour windows + burn rate + projection).
		if blocks {
			var reports []usage.BlockReport
			var err error
			if sessionID != "" {
				reports, err = usage.SessionBlocks(nil, sessionID, usage.CostModeCalculate, duration)
			} else {
				reports, err = usage.ProjectBlocks(nil, usage.CostModeCalculate, duration)
			}
			if err != nil {
				return fmt.Errorf("could not compute usage blocks: %w", err)
			}
			if jsonOutput {
				return printJSON(reports)
			}
			printBlocks(os.Stdout, reports, limit)
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
			s, err := usage.SummarizeSession(nil, sessionID, usage.CostModeCalculate)
			if err != nil {
				return fmt.Errorf("could not summarize session %q: %w", sessionID, err)
			}
			if jsonOutput || ccusageJSON {
				return printJSON(s)
			}
			printSummaryText(s)
			return nil
		}

		result, err := usage.ScanProjects(nil, usage.CostModeCalculate, since)
		if err != nil {
			return fmt.Errorf("could not scan projects: %w", err)
		}

		if ccusageJSON {
			return printJSON(toCcusageReport(result))
		}
		if jsonOutput {
			return printJSON(result)
		}
		printScanText(result)
		return nil
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&ccusageJSON, "ccusage-json", false, "Output the ccusage 'claude session --json' document shape")
	cmd.Flags().StringVar(&sessionID, "session", "", "Roll up a single session (parent + subagents + workflow)")
	cmd.Flags().StringVar(&sinceDur, "since", "", "Only count entries newer than this duration (e.g. 24h, 168h)")
	cmd.Flags().BoolVar(&blocks, "blocks", false, "Group usage into rolling 5-hour blocks with burn rate and projection")
	cmd.Flags().BoolVar(&watch, "watch", false, "Live-tail the active block (burn rate, projection); refreshes on a timer")
	cmd.Flags().Float64Var(&blockHours, "block-hours", 0, "Rolling block window in hours (default 5)")
	cmd.Flags().StringVar(&watchEvery, "watch-interval", "", "Refresh interval for --watch (default 2s)")
	cmd.Flags().Int64Var(&limit, "limit", 0, "Config-defined token denominator for the block projection (no live limits API)")

	return cmd
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
