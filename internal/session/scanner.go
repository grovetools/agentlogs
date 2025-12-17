package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/mattsolo1/grove-core/pkg/workspace"
)

// Scanner is responsible for finding and parsing session transcript logs.
type Scanner struct{}

// NewScanner creates a new session scanner.
func NewScanner() *Scanner {
	return &Scanner{}
}

// loadSessionRegistry scans the ~/.grove/hooks/sessions directory and builds a map
// of native agent session IDs to their structured metadata.
func (s *Scanner) loadSessionRegistry() (map[string]sessions.SessionMetadata, error) {
	registryMap := make(map[string]sessions.SessionMetadata)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Failed to get user home directory: %v\n", err)
		return nil, err
	}

	sessionsDir := filepath.Join(homeDir, ".grove", "hooks", "sessions")
	fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Scanning sessions directory: %s\n", sessionsDir)

	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Sessions directory does not exist\n")
		return registryMap, nil // No registry directory, nothing to load.
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Failed to read sessions directory: %v\n", err)
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Found %d entries in sessions directory\n", len(entries))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metadataPath := filepath.Join(sessionsDir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Skipping %s - no metadata file: %v\n", entry.Name(), err)
			continue // Skip sessions without metadata
		}

		var metadata sessions.SessionMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Skipping %s - invalid metadata: %v\n", entry.Name(), err)
			continue // Skip invalid metadata
		}

		// The key is the native agent session ID (e.g., Claude's UUID).
		// This is stored in ClaudeSessionID, while SessionID is the flow job ID.
		if metadata.ClaudeSessionID != "" {
			registryMap[metadata.ClaudeSessionID] = metadata
			fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Registered session %s -> %s (transcript: %s)\n",
				metadata.ClaudeSessionID, metadata.SessionID, metadata.TranscriptPath)
		} else {
			// Backwards compatibility for older metadata files
			registryMap[entry.Name()] = metadata
			fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Registered session %s (legacy format)\n", entry.Name())
		}
	}
	fmt.Fprintf(os.Stderr, "AGLOGS_REGISTRY: Loaded %d sessions from registry\n", len(registryMap))
	return registryMap, nil
}

// Scan searches for and parses all Claude and Codex session logs.
func (s *Scanner) Scan() ([]SessionInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// 1. Load the session registry first for reliable job association.
	registry, err := s.loadSessionRegistry()
	if err != nil {
		// Log a warning but proceed, allowing fallback to old method.
		fmt.Fprintf(os.Stderr, "Warning: could not load session registry: %v\n", err)
	}

	claudePattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
	claudeMatches, _ := filepath.Glob(claudePattern)

	codexPattern := filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "*.jsonl")
	codexMatches, _ := filepath.Glob(codexPattern)

	matches := append(claudeMatches, codexMatches...)
	fmt.Fprintf(os.Stderr, "AGLOGS_SCAN: Found %d Claude transcripts and %d Codex transcripts\n",
		len(claudeMatches), len(codexMatches))

	var sessions []SessionInfo

	for _, logPath := range matches {
		var sessionID, cwd string
		var startedAt time.Time
		var jobs []JobInfo
		found := false

		if strings.Contains(logPath, "/.codex/") {
			sessionID, cwd, startedAt, jobs, found = s.parseCodexLog(logPath)
		} else {
			sessionID, cwd, startedAt, jobs, found = s.parseClaudeLog(logPath)
		}

		fmt.Fprintf(os.Stderr, "AGLOGS_SCAN: Parsed transcript %s -> session_id=%s, found=%v\n",
			filepath.Base(logPath), sessionID, found)

		// 2. Prioritize data from the registry if available.
		if metadata, foundInRegistry := registry[sessionID]; foundInRegistry {
			fmt.Fprintf(os.Stderr, "AGLOGS_SCAN: Found session %s in registry (job: %s/%s)\n",
				sessionID, metadata.PlanName, filepath.Base(metadata.JobFilePath))
			// Use reliable data from the registry.
			projectPath, projectName, worktree, ecosystem := s.parseProjectPath(metadata.WorkingDirectory)

			// Create a JobInfo from the registry metadata.
			registryJobs := []JobInfo{}
			if metadata.PlanName != "" && metadata.JobFilePath != "" {
				// If we have jobs from log parsing, use the first one's LineIndex
				lineIndex := 0
				if len(jobs) > 0 {
					lineIndex = jobs[0].LineIndex
				}
				registryJobs = append(registryJobs, JobInfo{
					Plan:      metadata.PlanName,
					Job:       filepath.Base(metadata.JobFilePath),
					LineIndex: lineIndex,
				})
			}

			sessions = append(sessions, SessionInfo{
				SessionID:   sessionID,
				ProjectName: projectName,
				ProjectPath: projectPath,
				Worktree:    worktree,
				Ecosystem:   ecosystem,
				Jobs:        registryJobs,
				LogFilePath: logPath,
				StartedAt:   metadata.StartedAt,
			})
			continue // Skip to next log file
		}

		// 3. Fallback for logs not in the registry.
		if !found {
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

		projectPath, projectName, worktree, ecosystem := s.parseProjectPath(cwd)
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
	return sessions, nil
}

func (s *Scanner) parseProjectPath(cwd string) (projectPath, projectName, worktree, ecosystem string) {
	projInfo, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		projectName = filepath.Base(cwd)
		projectPath = cwd
		return
	}

	if projInfo.IsWorktree() {
		worktree = projInfo.Name
		if projInfo.ParentProjectPath != "" {
			projectPath = projInfo.ParentProjectPath
			projectName = filepath.Base(projInfo.ParentProjectPath)
		} else {
			projectPath = projInfo.Path
			projectName = projInfo.Name
		}
	} else {
		projectName = projInfo.Name
		projectPath = projInfo.Path
	}

	if projInfo.RootEcosystemPath != "" {
		ecosystem = filepath.Base(projInfo.RootEcosystemPath)
	} else if projInfo.ParentEcosystemPath != "" {
		ecosystem = filepath.Base(projInfo.ParentEcosystemPath)
	}
	return
}

func (s *Scanner) parsePlanInfo(content string) (plan, job string) {
	if strings.Contains(content, "Read the file") && strings.Contains(content, "and execute the agent job") {
		start := strings.Index(content, "/")
		if start == -1 {
			return "", ""
		}

		end := strings.Index(content[start:], " and")
		if end == -1 {
			end = strings.Index(content[start:], " ")
		}
		if end == -1 {
			return "", ""
		}

		path := content[start : start+end]

		if strings.Contains(path, "/plans/") && strings.HasSuffix(path, ".md") {
			parts := strings.Split(path, "/")
			if len(parts) >= 2 {
				job = parts[len(parts)-1]
				plan = parts[len(parts)-2]
			}
		}
	}
	return plan, job
}

func (s *Scanner) parseClaudeLog(logPath string) (sessionID, cwd string, startedAt time.Time, jobs []JobInfo, found bool) {
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
				if plan, job := s.parsePlanInfo(msg.Message.Content); plan != "" && job != "" {
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

func (s *Scanner) parseCodexLog(logPath string) (sessionID, cwd string, startedAt time.Time, jobs []JobInfo, found bool) {
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
										if plan, job := s.parsePlanInfo(text); plan != "" && job != "" {
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
