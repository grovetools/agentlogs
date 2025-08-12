package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/mattsolo1/grove-claude-logs/internal/transcript"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := cli.NewStandardCommand(
		"clogs",
		"Claude transcript log parsing and monitoring",
	)
	
	// Add subcommands
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newTailCmd())
	rootCmd.AddCommand(newQueryCmd())
	
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available session transcripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			
			claudeDir := filepath.Join(homeDir, ".claude", "projects")
			projects, err := os.ReadDir(claudeDir)
			if err != nil {
				return fmt.Errorf("failed to read Claude projects directory: %w", err)
			}
			
			var sessions []string
			for _, project := range projects {
				if !project.IsDir() {
					continue
				}
				
				projectPath := filepath.Join(claudeDir, project.Name())
				files, err := os.ReadDir(projectPath)
				if err != nil {
					continue
				}
				
				for _, file := range files {
					if strings.HasSuffix(file.Name(), ".jsonl") {
						sessionID := strings.TrimSuffix(file.Name(), ".jsonl")
						sessions = append(sessions, fmt.Sprintf("%s/%s", project.Name(), sessionID))
					}
				}
			}
			
			if len(sessions) == 0 {
				fmt.Println("No session transcripts found")
				return nil
			}
			
			fmt.Println("Available session transcripts:")
			for _, session := range sessions {
				fmt.Printf("  - %s\n", session)
			}
			
			return nil
		},
	}
	
	return cmd
}

func newTailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tail <session_id>",
		Short: "Tail and parse messages from a specific transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			
			transcriptPath, err := transcript.GetTranscriptPath(sessionID)
			if err != nil {
				return fmt.Errorf("failed to find transcript: %w", err)
			}
			
			parser := transcript.NewParser()
			messages, err := parser.ParseFile(transcriptPath)
			if err != nil {
				return fmt.Errorf("failed to parse transcript: %w", err)
			}
			
			// Display last 10 messages or all if less than 10
			start := 0
			if len(messages) > 10 {
				start = len(messages) - 10
			}
			
			fmt.Printf("Showing last %d messages from session %s:\n\n", len(messages)-start, sessionID)
			
			for i := start; i < len(messages); i++ {
				msg := messages[i]
				fmt.Printf("[%s] %s: %s\n\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)
			}
			
			return nil
		},
	}
	
	return cmd
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <session_id>",
		Short: "Query messages from a transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			role, _ := cmd.Flags().GetString("role")
			jsonOutput, _ := cmd.Flags().GetBool("json")
			
			transcriptPath, err := transcript.GetTranscriptPath(sessionID)
			if err != nil {
				return fmt.Errorf("failed to find transcript: %w", err)
			}
			
			parser := transcript.NewParser()
			messages, err := parser.ParseFile(transcriptPath)
			if err != nil {
				return fmt.Errorf("failed to parse transcript: %w", err)
			}
			
			// Filter by role if specified
			var filtered []transcript.ExtractedMessage
			for _, msg := range messages {
				if role == "" || msg.Role == role {
					filtered = append(filtered, msg)
				}
			}
			
			if jsonOutput {
				data, err := json.MarshalIndent(filtered, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal messages: %w", err)
				}
				fmt.Println(string(data))
			} else {
				fmt.Printf("Found %d messages", len(filtered))
				if role != "" {
					fmt.Printf(" with role '%s'", role)
				}
				fmt.Printf(" in session %s:\n\n", sessionID)
				
				for _, msg := range filtered {
					fmt.Printf("[%s] %s: %s\n\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)
				}
			}
			
			return nil
		},
	}
	
	cmd.Flags().String("role", "", "Filter by message role (user, assistant)")
	cmd.Flags().Bool("json", false, "Output in JSON format")
	
	return cmd
}