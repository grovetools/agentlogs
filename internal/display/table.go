package display

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/mattsolo1/grove-agent-logs/internal/session"
)

// PrintSessionsTable prints a list of sessions in a formatted table.
func PrintSessionsTable(sessions []session.SessionInfo, writer io.Writer) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tECOSYSTEM\tPROJECT\tWORKTREE\tJOBS\tSTARTED")
	for _, s := range sessions {
		jobsStr := ""
		if len(s.Jobs) > 0 {
			jobsStr = fmt.Sprintf("%s/%s", s.Jobs[0].Plan, s.Jobs[0].Job)
			if len(s.Jobs) > 1 {
				jobsStr += fmt.Sprintf(" (+%d more)", len(s.Jobs)-1)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.SessionID, s.Ecosystem, s.ProjectName, s.Worktree, jobsStr,
			s.StartedAt.Format("2006-01-02 15:04"))
	}
	w.Flush()
}
