package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mattsolo1/grove-agent-logs/internal/display"
	"github.com/mattsolo1/grove-agent-logs/internal/session"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var jsonOutput bool
	var projectFilter string

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List available session transcripts",
		Long:  "List available session transcripts, optionally filtered by project name",
		RunE: func(cmd *cobra.Command, args []string) error {
			scanner := session.NewScanner()
			sessions, err := scanner.Scan()
			if err != nil {
				return fmt.Errorf("failed to scan for sessions: %w", err)
			}
			if len(sessions) == 0 {
				fmt.Println("No session transcripts found.")
				return nil
			}

			// Filter by project if specified
			if projectFilter != "" {
				var filtered []session.SessionInfo
				for _, s := range sessions {
					if strings.Contains(strings.ToLower(s.ProjectName), strings.ToLower(projectFilter)) ||
						strings.Contains(strings.ToLower(s.Worktree), strings.ToLower(projectFilter)) {
						filtered = append(filtered, s)
						continue
					}

					for _, job := range s.Jobs {
						if strings.Contains(strings.ToLower(job.Plan), strings.ToLower(projectFilter)) ||
							strings.Contains(strings.ToLower(job.Job), strings.ToLower(projectFilter)) {
							filtered = append(filtered, s)
							break
						}
					}
				}
				sessions = filtered
			}

			if len(sessions) == 0 {
				if projectFilter != "" {
					fmt.Printf("No session transcripts found for project matching '%s'\n", projectFilter)
				} else {
					fmt.Println("No session transcripts found")
				}
				return nil
			}

			// Sort sessions by started time, most recent first
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].StartedAt.After(sessions[j].StartedAt)
			})

			if jsonOutput {
				data, err := json.MarshalIndent(sessions, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal sessions to JSON: %w", err)
				}
				fmt.Println(string(data))
			} else {
				display.PrintSessionsTable(sessions, os.Stdout)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVarP(&projectFilter, "project", "p", "", "Filter sessions by project, worktree, plan, or job name (case-insensitive substring match)")

	return cmd
}
