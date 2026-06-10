package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	"github.com/grovetools/agentlogs/pkg/agentstream"
	"github.com/grovetools/agentlogs/pkg/display"
)

func newWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow <session-id-or-dir>",
		Short: "Stream workflow subagent transcripts for a session",
		Long: "Tails journal.jsonl and every agent-*.jsonl under <session-dir>/subagents/workflows/wf_*/, " +
			"fanning all entries into a single stream tagged by agent ID. " +
			"<session-id-or-dir> can be a Claude session ID or a path to the session directory.",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // Internal command for now
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionDir, err := resolveSessionDir(args[0])
			if err != nil {
				return err
			}
			jsonOutput, _ := cmd.Flags().GetBool("json")

			ch, err := agentstream.StreamWorkflow(cmd.Context(), sessionDir)
			if err != nil {
				return fmt.Errorf("failed to stream workflow: %w", err)
			}

			jsonEncoder := json.NewEncoder(os.Stdout)
			toolFormatters := display.DefaultToolFormatters()
			agentStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)
			lastAgent := ""

			for entry := range ch {
				if jsonOutput {
					_ = jsonEncoder.Encode(entry)
					continue
				}
				if entry.AgentID != lastAgent {
					fmt.Println(agentStyle.Render(fmt.Sprintf("── agent %s [%s] ──", entry.AgentID, entry.Provider)))
					lastAgent = entry.AgentID
				}
				display.DisplayUnifiedEntry(entry, "full", toolFormatters)
			}

			return nil
		},
	}
	return cmd
}

// resolveSessionDir turns a session ID or directory path into a Claude
// session directory (the directory containing subagents/workflows/).
func resolveSessionDir(spec string) (string, error) {
	if info, err := os.Stat(spec); err == nil && info.IsDir() {
		return spec, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(homeDir, ".claude", "projects", "*", spec))
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.IsDir() {
			return match, nil
		}
	}
	return "", fmt.Errorf("could not resolve '%s' to a session directory", spec)
}
