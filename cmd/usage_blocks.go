package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/grovetools/agentlogs/pkg/usage"
)

// printBlocks renders rolling 5-hour usage blocks: one row per block, with the
// active block annotated by its burn rate, linear projection, and (when a limit
// denominator is given) a percent-of-limit status. Gap blocks render as idle
// stretches. limit <= 0 omits the limit columns.
func printBlocks(w io.Writer, reports []usage.BlockReport, limit int64) {
	if len(reports) == 0 {
		fmt.Fprintln(w, "No Claude usage data found.")
		return
	}
	for _, r := range reports {
		b := r.Block
		if b.IsGap {
			gap := b.EndTime.Sub(b.StartTime).Round(time.Minute)
			fmt.Fprintf(w, "  %s  (inactive — %s gap)\n",
				b.StartTime.Local().Format("01/02 15:04"), formatDuration(gap))
			continue
		}

		status := ""
		if b.IsActive {
			status = "ACTIVE"
		}
		fmt.Fprintf(w, "%s %s\n", b.StartTime.Local().Format("2006-01-02 15:04"), status)
		fmt.Fprintf(w, "  Tokens: %s   Cost: $%.4f   Models: %s\n",
			formatNumber(b.TotalTokens()), b.CostUSD, modelsLabel(b.Models))

		if b.IsActive {
			now := time.Now()
			elapsed := now.Sub(b.StartTime)
			remaining := b.EndTime.Sub(now)
			fmt.Fprintf(w, "  Elapsed: %s   Remaining: %s\n",
				formatDuration(elapsed), formatDuration(remaining))
			if r.Burn != nil {
				fmt.Fprintf(w, "  Burn: %s tok/min   $%.2f/hr\n",
					formatNumber(int64(r.Burn.TokensPerMinute+0.5)), r.Burn.CostPerHour)
			}
			if r.Projection != nil {
				fmt.Fprintf(w, "  Projected (block end): %s tokens   $%.2f\n",
					formatNumber(r.Projection.TotalTokens), r.Projection.TotalCost)
				if limit > 0 {
					projected := r.Projection.TotalTokens
					st := usage.EvaluateLimit(b.TotalTokens(), projected, limit)
					fmt.Fprintf(w, "  Limit: %s tokens   Projected %.1f%% [%s]\n",
						formatNumber(limit), st.PercentUsed, strings.ToUpper(string(st.State)))
				}
			}
		}
		fmt.Fprintln(w)
	}
}

// runUsageWatch live-tails the active 5-hour block, redrawing the block view on
// a timer until interrupted (Ctrl-C). When sessionID is set it tails that one
// session's files (parent + ad-hoc + workflow); otherwise it tails every
// project. The poll re-reads the transcripts each tick — SummarizeTranscript is
// cheap and incremental-friendly, and Claude appends to the same files.
func runUsageWatch(parent context.Context, sessionID string, duration time.Duration, limit int64, every time.Duration) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	render := func() error {
		var reports []usage.BlockReport
		var err error
		if sessionID != "" {
			reports, err = usage.SessionBlocks(nil, sessionID, usage.CostModeCalculate, duration)
		} else {
			reports, err = usage.ProjectBlocks(nil, usage.CostModeCalculate, duration)
		}
		if err != nil {
			return err
		}
		clearScreen(os.Stdout)
		fmt.Fprintf(os.Stdout, "aglogs usage --watch   %s   (Ctrl-C to exit)\n\n",
			time.Now().Format("2006-01-02 15:04:05"))
		active := activeReport(reports)
		if active == nil {
			fmt.Fprintln(os.Stdout, "No active block — the last conversation is older than the")
			fmt.Fprintln(os.Stdout, "session window; its prompt cache has likely expired (will re-cache).")
			return nil
		}
		printBlocks(os.Stdout, []usage.BlockReport{*active}, limit)
		return nil
	}

	if err := render(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stdout)
			return nil
		case <-ticker.C:
			if err := render(); err != nil {
				return err
			}
		}
	}
}

// activeReport returns the active block report, or nil when none is active.
func activeReport(reports []usage.BlockReport) *usage.BlockReport {
	for i := range reports {
		if reports[i].Block.IsActive {
			return &reports[i]
		}
	}
	return nil
}

// clearScreen issues the ANSI clear+home sequence for the live watcher.
func clearScreen(w io.Writer) {
	fmt.Fprint(w, "\033[2J\033[H")
}

// modelsLabel renders a model list for a block row, "-" when empty.
func modelsLabel(models []string) string {
	if len(models) == 0 {
		return "-"
	}
	return strings.Join(models, ", ")
}

// formatDuration renders a duration as "Hh Mm" (or "Mm" under an hour).
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	mins := int64(d.Minutes())
	h := mins / 60
	m := mins % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// formatNumber renders an int with thousands separators (1234567 -> 1,234,567).
func formatNumber(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	out := b.String()
	if neg {
		return "-" + out
	}
	return out
}
