package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattsolo1/grove-agent-logs/cmd"
	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/spf13/cobra"
)

// JobInfo holds information about a grove plan job found in the transcript
type JobInfo struct {
	Plan      string `json:"plan"`
	Job       string `json:"job"`
	LineIndex int    `json:"lineIndex"`
}

// SessionInfo holds structured information about a session transcript
type SessionInfo struct {
	SessionID   string    `json:"sessionId"`
	ProjectName string    `json:"projectName"`
	ProjectPath string    `json:"projectPath"`
	Worktree    string    `json:"worktree,omitempty"`
	Ecosystem   string    `json:"ecosystem,omitempty"`
	Jobs        []JobInfo `json:"jobs,omitempty"`
	LogFilePath string    `json:"logFilePath"`
	StartedAt   time.Time `json:"startedAt"`
}

// parseProjectPath extracts project information using grove-core workspace package
func parseProjectPath(cwd string) (projectPath, projectName, worktree, ecosystem string) {
	// Use workspace.GetProjectByPath for robust project discovery
	projInfo, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		// Fallback for paths that no longer exist or other errors
		projectName = filepath.Base(cwd)
		projectPath = cwd
		worktree = ""
		ecosystem = ""
		return
	}

	if projInfo.IsWorktree() {
		worktree = projInfo.Name
		if projInfo.ParentProjectPath != "" {
			projectPath = projInfo.ParentProjectPath
			projectName = filepath.Base(projInfo.ParentProjectPath)
		} else {
			// Fallback for worktrees without a parent project path.
			// This might occur for an ecosystem worktree where the parent is the ecosystem itself.
			projectPath = projInfo.Path
			projectName = projInfo.Name
		}
	} else {
		projectName = projInfo.Name
		projectPath = projInfo.Path
		worktree = ""
	}

	if projInfo.RootEcosystemPath != "" {
		ecosystem = filepath.Base(projInfo.RootEcosystemPath)
	} else if projInfo.ParentEcosystemPath != "" {
		ecosystem = filepath.Base(projInfo.ParentEcosystemPath)
	}
	return
}

// parsePlanInfo extracts plan and job information from a message content
func parsePlanInfo(content string) (plan, job string) {
	// Look for the pattern "Read the file <path> and execute the agent job"
	if strings.Contains(content, "Read the file") && strings.Contains(content, "and execute the agent job") {
		// Extract the file path
		start := strings.Index(content, "/")
		if start == -1 {
			return "", ""
		}
		
		// Find the end of the path (space or "and")
		end := strings.Index(content[start:], " and")
		if end == -1 {
			end = strings.Index(content[start:], " ")
		}
		if end == -1 {
			return "", ""
		}
		
		path := content[start : start+end]
		
		// Check if this is a plan file path
		if strings.Contains(path, "/plans/") && strings.HasSuffix(path, ".md") {
			parts := strings.Split(path, "/")
			if len(parts) >= 2 {
				// Get the job filename (last part)
				job = parts[len(parts)-1]
				// Get the plan name (second to last part)
				plan = parts[len(parts)-2]
			}
		}
	}
	return plan, job
}

func main() {
	rootCmd := cli.NewStandardCommand(
		"aglogs",
		"Agent transcript log parsing and monitoring",
	)
	
	// Add subcommands
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newTailCmd())
	rootCmd.AddCommand(newQueryCmd())
	rootCmd.AddCommand(newReadCmd())
	rootCmd.AddCommand(newGetSessionInfoCmd())
	rootCmd.AddCommand(cmd.NewVersionCmd())
	
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newListCmd() *cobra.Command {
	var jsonOutput bool
	var projectFilter string
	
	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List available session transcripts",
		Long:  "List available session transcripts, optionally filtered by project name",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			
			// Search both Claude and Codex log directories
			claudePattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
			claudeMatches, _ := filepath.Glob(claudePattern)

			codexPattern := filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "*.jsonl")
			codexMatches, _ := filepath.Glob(codexPattern)

			matches := append(claudeMatches, codexMatches...)
			if len(matches) == 0 {
				fmt.Println("No session transcripts found.")
				return nil
			}

			var sessions []SessionInfo
			for _, logPath := range matches {
				var sessionID, cwd string
				var startedAt time.Time
				var jobs []JobInfo
				found := false

				if strings.Contains(logPath, "/.codex/") {
					// Use Codex log parser
					sessionID, cwd, startedAt, jobs, found = parseCodexLog(logPath)
				} else {
					// Use existing Claude log parser
					sessionID, cwd, startedAt, jobs, found = parseClaudeLog(logPath)
				}

				if !found {
					// Fallback for files where we can't find the info
					stat, err := os.Stat(logPath)
					if err != nil { 
						continue 
					}
					sessions = append(sessions, SessionInfo{
						SessionID:   strings.TrimSuffix(filepath.Base(logPath), ".jsonl"),
						ProjectName: "unknown",
						ProjectPath: "unknown",
						Worktree:    "",
						Jobs:        []JobInfo{},
						LogFilePath: logPath,
						StartedAt:   stat.ModTime(),
					})
					continue
				}

				projectPath, projectName, worktree, ecosystem := parseProjectPath(cwd)
				sessions = append(sessions, SessionInfo{
					SessionID:   sessionID,
					ProjectName: projectName,
					ProjectPath: projectPath,
					Worktree:    worktree,
					Ecosystem:   ecosystem,
					Jobs:        jobs,
					LogFilePath: logPath,
					StartedAt:   startedAt,
				})
			}

			// Filter by project if specified
			if projectFilter != "" {
				var filtered []SessionInfo
				for _, s := range sessions {
					// Check project name and worktree name
					if strings.Contains(strings.ToLower(s.ProjectName), strings.ToLower(projectFilter)) ||
						strings.Contains(strings.ToLower(s.Worktree), strings.ToLower(projectFilter)) {
						filtered = append(filtered, s)
						continue
					}
					
					// Check job plans
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
				// Output as JSON
				data, err := json.MarshalIndent(sessions, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal sessions to JSON: %w", err)
				}
				fmt.Println(string(data))
			} else {
				// Print formatted table
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
			
			return nil
		},
	}
	
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVarP(&projectFilter, "project", "p", "", "Filter sessions by project, worktree, plan, or job name (case-insensitive substring match)")
	
	return cmd
}

func newTailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tail <session_id>",
		Short: "Tail and parse messages from a specific transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			transcriptPath, err := transcript.GetTranscriptPathLegacy(sessionID)
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

			transcriptPath, err := transcript.GetTranscriptPathLegacy(sessionID)
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

func newReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read <plan/job>",
		Short: "Read logs for a specific plan/job execution",
		Long:  "Read logs starting from a specific plan/job execution until the next job or end of session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobSpec := args[0]
			
			// Parse the job specification (plan/job)
			parts := strings.Split(jobSpec, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid job specification: expected format 'plan/job.md'")
			}
			planName := parts[0]
			jobName := parts[1]
			
			// Get session ID and project filter if specified
			sessionID, _ := cmd.Flags().GetString("session")
			projectFilter, _ := cmd.Flags().GetString("project")
			
			// Find matching sessions
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			
			// Search both Claude and Codex log directories
			claudePattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
			claudeMatches, _ := filepath.Glob(claudePattern)

			codexPattern := filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "*.jsonl")
			codexMatches, _ := filepath.Glob(codexPattern)

			matches := append(claudeMatches, codexMatches...)
			if len(matches) == 0 {
				return fmt.Errorf("no session transcripts found")
			}

			// First, group all log files by their actual session ID
			type logFileInfo struct {
				logPath     string
				sessionID   string
				projectName string
				cwd         string
				jobs        []JobInfo
			}

			logsBySession := make(map[string][]logFileInfo)

			for _, logPath := range matches {
				// If session ID is specified, filter by filename
				if sessionID != "" {
					baseName := strings.TrimSuffix(filepath.Base(logPath), ".jsonl")
					if !strings.Contains(baseName, sessionID) {
						continue
					}
				}

				file, err := os.Open(logPath)
				if err != nil {
					continue
				}

				// Scan for jobs and session info
				var actualSessionID, cwd string
				var jobs []JobInfo
				var foundInfo bool

				if strings.Contains(logPath, "/.codex/") {
					actualSessionID, cwd, _, jobs, foundInfo = parseCodexLog(logPath)
				} else {
					actualSessionID, cwd, _, jobs, foundInfo = parseClaudeLog(logPath)
				}
				file.Close()

				if !foundInfo {
					continue
				}

				// Apply project filter if specified
				if projectFilter != "" {
					_, projectName, _, _ := parseProjectPath(cwd)
					if !strings.Contains(strings.ToLower(projectName), strings.ToLower(projectFilter)) {
						continue
					}
				}

				_, projectName, _, _ := parseProjectPath(cwd)

				logsBySession[actualSessionID] = append(logsBySession[actualSessionID], logFileInfo{
					logPath:     logPath,
					sessionID:   actualSessionID,
					projectName: projectName,
					cwd:         cwd,
					jobs:        jobs,
				})
			}

			// Now find which sessions contain the job we're looking for
			type jobMatch struct {
				sessionID   string
				projectName string
				logPath     string
				job         JobInfo
				nextJobLine int
			}
			var found []jobMatch

			for sid, logFiles := range logsBySession {
				// Check all log files in this session for the job
				for _, logFile := range logFiles {
					for i, job := range logFile.jobs {
						if job.Plan == planName && job.Job == jobName {
							nextLine := -1
							if i+1 < len(logFile.jobs) {
								nextLine = logFile.jobs[i+1].LineIndex
							}

							// Add this log file AND all other log files from the same session
							for _, sessionLogFile := range logFiles {
								found = append(found, jobMatch{
									sessionID:   sid,
									projectName: sessionLogFile.projectName,
									logPath:     sessionLogFile.logPath,
									job:         job,
									nextJobLine: nextLine,
								})
							}
							goto nextSession // Found the job in this session, move to next session
						}
					}
				}
			nextSession:
			}

			if len(found) == 0 {
				return fmt.Errorf("no sessions found with job %s", jobSpec)
			}

			// Group matches by session ID
			sessionGroups := make(map[string][]jobMatch)
			for _, match := range found {
				sessionGroups[match.sessionID] = append(sessionGroups[match.sessionID], match)
			}

			// If multiple DIFFERENT sessions and no session specified, show options
			if len(sessionGroups) > 1 && sessionID == "" {
				fmt.Printf("Multiple sessions found with job %s:\n\n", jobSpec)
				for sid, matches := range sessionGroups {
					match := matches[0] // Use first match for display
					fmt.Printf("  Project: %s\n", match.projectName)
					fmt.Printf("  Session: %s (%d log file(s))\n\n", sid, len(matches))
				}
				fmt.Println("Please specify a session with --session or filter by project with --project")
				return nil
			}

			// Get the session group to use (first if only one, or matched by session ID)
			var matchesToUse []jobMatch
			if len(sessionGroups) == 1 {
				for _, matches := range sessionGroups {
					matchesToUse = matches
					break
				}
			} else {
				// Find by session ID
				for sid, matches := range sessionGroups {
					if strings.Contains(sid, sessionID) {
						matchesToUse = matches
						break
					}
				}
			}

			if len(matchesToUse) == 0 {
				return fmt.Errorf("no matching session found")
			}

			// Use the first match for metadata
			match := matchesToUse[0]

			fmt.Printf("=== Job: %s/%s ===\n", match.job.Plan, match.job.Job)
			fmt.Printf("Project: %s\n", match.projectName)
			fmt.Printf("Session: %s\n", match.sessionID)
			if len(matchesToUse) > 1 {
				fmt.Printf("Log files: %d (resumed session)\n", len(matchesToUse))
			}
			fmt.Printf("Starting at line: %d\n\n", match.job.LineIndex)

			// Read and display logs from ALL matches (handles resumed sessions with multiple log files)
			for matchIdx, currentMatch := range matchesToUse {
				file, err := os.Open(currentMatch.logPath)
				if err != nil {
					continue // Skip files we can't open
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				// Increase buffer size for large JSON lines (matching parser.go)
				const maxScanTokenSize = 1024 * 1024 // 1MB
				buf := make([]byte, 0, 64*1024)
				scanner.Buffer(buf, maxScanTokenSize)

				lineIndex := 0
				inRange := false

				// For the first log file, start from the job's line index
				// For subsequent log files (resumed sessions), start from the beginning
				startLine := 0
				if matchIdx == 0 {
					startLine = currentMatch.job.LineIndex
				}

				for scanner.Scan() {
					if lineIndex == startLine {
						inRange = true
					}

					if currentMatch.nextJobLine != -1 && lineIndex >= currentMatch.nextJobLine {
						break
					}

					if inRange {
						line := scanner.Bytes()
						if len(line) > 0 {
							if strings.Contains(currentMatch.logPath, "/.codex/") {
								// Parse and display Codex log line
								displayCodexLogLine(line)
							} else {
								// Try to parse as a Claude transcript entry
								var entry transcript.TranscriptEntry
								if err := json.Unmarshal(line, &entry); err == nil {
									// Extract message content if it's a user or assistant message
									if (entry.Type == "user" || entry.Type == "assistant") && entry.Message != nil {
										// Handle both string and array content formats
										var textContent string
										var toolUses []string

										// Try string content first (for user messages)
										var stringContent string
										if err := json.Unmarshal(entry.Message.Content, &stringContent); err == nil {
											textContent = stringContent
										} else {
											// Try array content (for assistant messages)
											var contentArray []json.RawMessage
											if err := json.Unmarshal(entry.Message.Content, &contentArray); err == nil {
												for _, rawContent := range contentArray {
													var content struct {
														Type  string          `json:"type"`
														Text  string          `json:"text"`
														Name  string          `json:"name"`
														Input json.RawMessage `json:"input"`
													}
													if err := json.Unmarshal(rawContent, &content); err == nil {
														if content.Type == "text" {
															if textContent != "" {
																textContent += "\n"
															}
															textContent += content.Text
														} else if content.Type == "tool_use" {
															// Extract tool name and key inputs
															toolInfo := fmt.Sprintf("[Using %s", content.Name)

															// Try to extract common input fields
															var inputs map[string]interface{}
															if err := json.Unmarshal(content.Input, &inputs); err == nil {
																// Show file paths, commands, or other key parameters
																if filePath, ok := inputs["file_path"].(string); ok {
																	toolInfo += fmt.Sprintf(" on %s", filePath)
																} else if command, ok := inputs["command"].(string); ok {
																	// Truncate long commands
																	if len(command) > 50 {
																		toolInfo += fmt.Sprintf(": %s...", command[:50])
																	} else {
																		toolInfo += fmt.Sprintf(": %s", command)
																	}
																} else if pattern, ok := inputs["pattern"].(string); ok {
																	toolInfo += fmt.Sprintf(" for '%s'", pattern)
																}
															}
															toolInfo += "]"
															toolUses = append(toolUses, toolInfo)
														}
													}
												}
											}
										}

										// Display tool uses if any
										if len(toolUses) > 0 {
											role := "Agent"
											for _, toolUse := range toolUses {
												fmt.Printf("%s: %s\n", role, toolUse)
											}
											if textContent != "" {
												fmt.Println() // Add space between tools and text
											}
										}

										// Display text content
										if textContent != "" {
											role := entry.Type
											if role == "assistant" {
												role = "Agent"
											} else if role == "user" {
												role = "User"
											}
											fmt.Printf("%s: %s\n\n", role, textContent)
										}
									}
								}
							}
						}
					}

					lineIndex++
				}
			}

			// Show end marker after processing all log files
			lastMatch := matchesToUse[len(matchesToUse)-1]
			if lastMatch.nextJobLine != -1 {
				fmt.Printf("\n=== Next job starts at line %d ===\n", lastMatch.nextJobLine)
			} else {
				fmt.Println("\n=== End of session ===")
			}
			
			return nil
		},
	}
	
	cmd.Flags().StringP("session", "s", "", "Specify session ID (required if multiple matches)")
	cmd.Flags().StringP("project", "p", "", "Filter by project name")

	return cmd
}

func newGetSessionInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "get-session-info <job-file>",
		Short:  "Get session details for a given job file",
		Long:   "Retrieves the native agent session ID and provider for a given Grove job file path from the sessions database or transcript logs.",
		Hidden: true, // Internal command for now
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobFilePath := args[0]

			// Extract plan name and job filename from the path
			// Path format: /path/to/plans/PLANNAME/NN-jobname.md
			parts := strings.Split(jobFilePath, string(filepath.Separator))
			if len(parts) < 2 {
				return fmt.Errorf("invalid job file path format: %s", jobFilePath)
			}
			jobFilename := parts[len(parts)-1]
			planName := parts[len(parts)-2]

			var agentSessionID, provider string

			// First, try the fast path: check the session registry by reading the job file's frontmatter
			// to get the job ID, then look up in registry
			if content, err := os.ReadFile(jobFilePath); err == nil {
				// Extract job ID from frontmatter (simple regex for "id: <value>")
				idRegex := regexp.MustCompile(`(?m)^id:\s*(.+)$`)
				if matches := idRegex.FindStringSubmatch(string(content)); len(matches) > 1 {
					jobID := strings.TrimSpace(matches[1])

					registry, err := sessions.NewFileSystemRegistry()
					if err == nil {
						session, err := registry.Find(jobID)
						if err == nil && session.ClaudeSessionID != "" {
							agentSessionID = session.ClaudeSessionID
							provider = session.Provider
						}
					}
				}
			}

			// Fallback: search transcript logs if not found in registry
			if agentSessionID == "" {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home directory: %w", err)
				}

				// Search both Claude and Codex log directories
				claudePattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
				claudeMatches, _ := filepath.Glob(claudePattern)

				codexPattern := filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "*.jsonl")
				codexMatches, _ := filepath.Glob(codexPattern)

				matches := append(claudeMatches, codexMatches...)

				// Search through transcripts for the plan/job combination
				for _, logPath := range matches {
					var sessionID string
					var jobs []JobInfo
					var found bool

					if strings.Contains(logPath, "/.codex/") {
						sessionID, _, _, jobs, found = parseCodexLog(logPath)
						if !found {
							continue
						}
						provider = "codex"
					} else {
						sessionID, _, _, jobs, found = parseClaudeLog(logPath)
						if !found {
							continue
						}
						provider = "claude"
					}

					// Check if any job in this session matches the plan/job filename
					for _, job := range jobs {
						if job.Plan == planName && job.Job == jobFilename {
							agentSessionID = sessionID
							break
						}
					}

					if agentSessionID != "" {
						break
					}
				}

				if agentSessionID == "" {
					return fmt.Errorf("could not find session for job %s/%s in registry or transcript logs", planName, jobFilename)
				}
			}

			// Output as JSON for easy parsing by grove-flow
			output := struct {
				AgentSessionID string `json:"agent_session_id"`
				Provider       string `json:"provider"`
			}{
				AgentSessionID: agentSessionID,
				Provider:       provider,
			}

			jsonData, err := json.Marshal(output)
			if err != nil {
				return fmt.Errorf("failed to marshal session info to JSON: %w", err)
			}

			fmt.Println(string(jsonData))
			return nil
		},
	}
	return cmd
}

// Helper functions for parsing different log formats

func parseClaudeLog(logPath string) (sessionID, cwd string, startedAt time.Time, jobs []JobInfo, found bool) {
	file, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer file.Close()

	jobMap := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	const maxScanTokenSize = 1024 * 1024 // 1MB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScanTokenSize)
	lineIndex := 0

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			lineIndex++
			continue
		}

		var msg struct {
			Cwd       string    `json:"cwd"`
			SessionID string    `json:"sessionId"`
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Message   struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}

		if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
			if !found && msg.Cwd != "" && msg.SessionID != "" && !msg.Timestamp.IsZero() {
				sessionID = msg.SessionID
				cwd = msg.Cwd
				startedAt = msg.Timestamp
				found = true
			}

			if msg.Type == "user" && msg.Message.Role == "user" {
				if plan, job := parsePlanInfo(msg.Message.Content); plan != "" && job != "" {
					key := plan + ":" + job
					if !jobMap[key] {
						jobMap[key] = true
						jobs = append(jobs, JobInfo{Plan: plan, Job: job, LineIndex: lineIndex})
					}
				}
			}
		}
		lineIndex++
		if lineIndex > 100 { // Performance limit
			break
		}
	}
	return
}

func parseCodexLog(logPath string) (sessionID, cwd string, startedAt time.Time, jobs []JobInfo, found bool) {
	file, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer file.Close()

	jobMap := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	const maxScanTokenSize = 1024 * 1024 // 1MB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScanTokenSize)
	lineIndex := 0

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			lineIndex++
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			lineIndex++
			continue
		}

		if entry["type"] == "session_meta" {
			if payload, ok := entry["payload"].(map[string]interface{}); ok {
				if id, ok := payload["id"].(string); ok {
					sessionID = id
				}
				if ts, ok := payload["timestamp"].(string); ok {
					startedAt, _ = time.Parse(time.RFC3339Nano, ts)
				}
			}
		}

		if entry["type"] == "response_item" {
			if payload, ok := entry["payload"].(map[string]interface{}); ok {
				if ptype, ok := payload["type"].(string); ok && ptype == "message" && payload["role"] == "user" {
					if content, ok := payload["content"].([]interface{}); ok {
						for _, c := range content {
							if cMap, ok := c.(map[string]interface{}); ok && cMap["type"] == "input_text" {
								if text, ok := cMap["text"].(string); ok {
									if strings.Contains(text, "<environment_context>") {
										re := regexp.MustCompile(`<cwd>(.*)</cwd>`)
										matches := re.FindStringSubmatch(text)
										if len(matches) > 1 {
											cwd = matches[1]
										}
									} else {
										if plan, job := parsePlanInfo(text); plan != "" && job != "" {
											key := plan + ":" + job
											if !jobMap[key] {
												jobMap[key] = true
												jobs = append(jobs, JobInfo{Plan: plan, Job: job, LineIndex: lineIndex})
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}

		if sessionID != "" && cwd != "" {
			found = true
		}

		lineIndex++
		if lineIndex > 100 { // Performance limit
			break
		}
	}
	return
}

func displayCodexLogLine(line []byte) {
	var entry map[string]interface{}
	if err := json.Unmarshal(line, &entry); err != nil {
		return // Skip lines that aren't valid JSON
	}

	payload, ok := entry["payload"].(map[string]interface{})
	if !ok {
		return
	}

	entryType, _ := payload["type"].(string)

	switch entryType {
	case "message":
		role, _ := payload["role"].(string)
		contentList, _ := payload["content"].([]interface{})
		var textContent string
		for _, c := range contentList {
			if cMap, ok := c.(map[string]interface{}); ok {
				if cType, ok := cMap["type"].(string); ok && cType == "input_text" {
					if text, ok := cMap["text"].(string); ok {
						textContent += text
					}
				}
			}
		}
		if textContent != "" && !strings.Contains(textContent, "<environment_context>") {
			roleDisplay := "User"
			if role == "assistant" {
				roleDisplay = "Agent"
			}
			fmt.Printf("%s: %s\n\n", roleDisplay, textContent)
		}
	case "agent_message":
		if message, ok := payload["message"].(string); ok {
			fmt.Printf("Agent: %s\n\n", message)
		}
	case "agent_reasoning":
		if text, ok := payload["text"].(string); ok {
			fmt.Printf("[Reasoning: %s]\n\n", text)
		}
	case "tool_code":
		if code, ok := payload["code"].(string); ok {
			lang, _ := payload["language"].(string)
			fmt.Printf("[Tool (%s)]:\n%s\n\n", lang, code)
		}
	}
}