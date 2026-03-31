package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/grovetools/agentlogs/internal/session"
)

// PrintSessionsTable prints a list of sessions in a formatted table.
func PrintSessionsTable(sessions []session.SessionInfo, writer io.Writer) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tPROVIDER\tECOSYSTEM\tPROJECT\tWORKTREE\tJOBS\tSTARTED")
	for _, s := range sessions {
		jobsStr := ""
		if len(s.Jobs) > 0 {
			jobsStr = fmt.Sprintf("%s/%s", s.Jobs[0].Plan, s.Jobs[0].Job)
			if len(s.Jobs) > 1 {
				jobsStr += fmt.Sprintf(" (+%d more)", len(s.Jobs)-1)
			}
		}

		// Determine provider display
		provider := s.Provider
		if provider == "" {
			// Infer provider from log file path for backwards compatibility
			if s.LogFilePath != "" {
				switch {
				case strings.Contains(s.LogFilePath, "/.codex/"):
					provider = "codex"
				case strings.Contains(s.LogFilePath, "/.claude/"):
					provider = "claude"
				}
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.SessionID, provider, s.Ecosystem, s.ProjectName, s.Worktree, jobsStr,
			s.StartedAt.Format("2006-01-02 15:04"))
	}
	w.Flush()
}
