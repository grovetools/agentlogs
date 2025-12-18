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

	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/logging"
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
	logger := logging.NewLogger("aglogs-registry")
	registryMap := make(map[string]sessions.SessionMetadata)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.WithError(err).Error("Failed to get user home directory")
		return nil, err
	}

	sessionsDir := filepath.Join(homeDir, ".grove", "hooks", "sessions")
	logger.WithField("sessions_dir", sessionsDir).Debug("Scanning sessions directory")

	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		logger.Debug("Sessions directory does not exist")
		return registryMap, nil // No registry directory, nothing to load.
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		logger.WithError(err).Error("Failed to read sessions directory")
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	logger.WithField("entry_count", len(entries)).Debug("Found entries in sessions directory")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metadataPath := filepath.Join(sessionsDir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			logger.WithFields(map[string]interface{}{
				"session_id":    entry.Name(),
				"metadata_path": metadataPath,
			}).Debug("Skipping session - no metadata file")
			continue // Skip sessions without metadata
		}

		var metadata sessions.SessionMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			logger.WithFields(map[string]interface{}{
				"session_id": entry.Name(),
				"error":      err,
			}).Warn("Skipping session - invalid metadata")
			continue // Skip invalid metadata
		}

		// The key is the native agent session ID (e.g., Claude's UUID).
		// This is stored in ClaudeSessionID, while SessionID is the flow job ID.
		if metadata.ClaudeSessionID != "" {
			registryMap[metadata.ClaudeSessionID] = metadata
			logger.WithFields(map[string]interface{}{
				"claude_session_id": metadata.ClaudeSessionID,
				"job_session_id":    metadata.SessionID,
				"transcript_path":   metadata.TranscriptPath,
				"plan_name":         metadata.PlanName,
				"job_file_path":     metadata.JobFilePath,
			}).Debug("Registered session from metadata")
		} else {
			// Backwards compatibility for older metadata files
			registryMap[entry.Name()] = metadata
			logger.WithField("session_id", entry.Name()).Debug("Registered session (legacy format)")
		}
	}
	logger.WithField("total_sessions", len(registryMap)).Debug("Loaded sessions from registry")
	return registryMap, nil
}

// Scan searches for and parses all Claude and Codex session logs.
func (s *Scanner) Scan() ([]SessionInfo, error) {
	logger := logging.NewLogger("aglogs-scan")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.WithError(err).Error("Failed to get user home directory")
		return nil, err
	}

	// 1. Load the session registry first for reliable job association.
	registry, err := s.loadSessionRegistry()
	if err != nil {
		// Log a warning but proceed, allowing fallback to old method.
		logger.WithError(err).Warn("Could not load session registry, proceeding with fallback")
	}

	// 1.5. Scan for archived sessions in plan artifact directories.
	archivedSessions, err := s.scanForArchivedSessions()
	if err != nil {
		logger.WithError(err).Warn("Could not scan for archived sessions, proceeding with live sessions only")
	}

	claudePattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
	claudeMatchesRaw, _ := filepath.Glob(claudePattern)

	// Filter out agent sidechain files (e.g., agent-*.jsonl)
	// These are Claude's internal sub-agents, not main sessions
	var claudeMatches []string
	for _, match := range claudeMatchesRaw {
		if !strings.HasPrefix(filepath.Base(match), "agent-") {
			claudeMatches = append(claudeMatches, match)
		}
	}

	codexPattern := filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "*.jsonl")
	codexMatches, _ := filepath.Glob(codexPattern)

	matches := append(claudeMatches, codexMatches...)
	logger.WithFields(map[string]interface{}{
		"claude_count": len(claudeMatches),
		"codex_count":  len(codexMatches),
		"total":        len(matches),
	}).Debug("Found transcript files")

	var sessions []SessionInfo
	// Track which registry sessions we've already added to avoid duplicates
	// (multiple .jsonl files like agent sidechains can have the same sessionID)
	processedRegistrySessions := make(map[string]bool)

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

		logger.WithFields(map[string]interface{}{
			"transcript_file": filepath.Base(logPath),
			"session_id":      sessionID,
			"found":           found,
		}).Debug("Parsed transcript file")

		// 2. Prioritize data from the registry if available.
		if metadata, foundInRegistry := registry[sessionID]; foundInRegistry {
			// Skip if we've already processed this registry session
			// (prevents duplicates from agent sidechain files)
			if processedRegistrySessions[sessionID] {
				logger.WithFields(map[string]interface{}{
					"session_id":      sessionID,
					"transcript_file": filepath.Base(logPath),
				}).Debug("Skipping duplicate registry session")
				continue
			}
			processedRegistrySessions[sessionID] = true
			logger.WithFields(map[string]interface{}{
				"session_id":    sessionID,
				"plan_name":     metadata.PlanName,
				"job_file_path": metadata.JobFilePath,
			}).Debug("Found session in registry, using metadata")
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

			// Use TranscriptPath from metadata if available, otherwise fallback to logPath
			// This ensures we use the main session file, not agent sidechain files
			transcriptPath := logPath
			if metadata.TranscriptPath != "" {
				transcriptPath = metadata.TranscriptPath
			}

			sessions = append(sessions, SessionInfo{
				SessionID:   sessionID,
				ProjectName: projectName,
				ProjectPath: projectPath,
				Worktree:    worktree,
				Ecosystem:   ecosystem,
				Jobs:        registryJobs,
				LogFilePath: transcriptPath,
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

	// 4. Merge archived sessions, avoiding duplicates with live sessions.
	for _, archivedSession := range archivedSessions {
		// If a session with this ID was already processed from the live registry, skip it.
		if _, exists := processedRegistrySessions[archivedSession.SessionID]; exists {
			continue
		}
		sessions = append(sessions, archivedSession)
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

// scanForArchivedSessions finds sessions archived in plan artifact directories.
func (s *Scanner) scanForArchivedSessions() ([]SessionInfo, error) {
	var archivedSessions []SessionInfo
	logger := logging.NewLogger("aglogs-archive-scan")

	// 1. Use grove-core to find all plan directories.
	coreCfg, err := config.LoadDefault()
	if err != nil {
		coreCfg = &config.Config{} // Proceed with defaults
	}
	discoveryService := workspace.NewDiscoveryService(logger.Logger)
	discoveryResult, err := discoveryService.DiscoverAll()
	if err != nil {
		return nil, fmt.Errorf("workspace discovery failed: %w", err)
	}
	provider := workspace.NewProvider(discoveryResult)
	locator := workspace.NewNotebookLocator(coreCfg)
	scannedDirs, err := locator.ScanForAllPlans(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to scan for plans: %w", err)
	}

	// 2. For each plan directory, search for archived sessions.
	for _, scannedDir := range scannedDirs {
		artifactsDir := filepath.Join(scannedDir.Path, ".artifacts")
		jobDirs, err := os.ReadDir(artifactsDir)
		if err != nil {
			continue
		}

		for _, jobEntry := range jobDirs {
			if !jobEntry.IsDir() {
				continue
			}

			metadataPath := filepath.Join(artifactsDir, jobEntry.Name(), "metadata.json")
			if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
				continue
			}

			// 3. Parse metadata and construct SessionInfo.
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				continue
			}
			var metadata sessions.SessionMetadata
			if err := json.Unmarshal(data, &metadata); err != nil {
				continue
			}

			transcriptPath := filepath.Join(artifactsDir, jobEntry.Name(), "transcript.jsonl")

			// Construct a JobInfo from the metadata
			jobInfo := []JobInfo{}
			if metadata.PlanName != "" && metadata.JobFilePath != "" {
				jobInfo = append(jobInfo, JobInfo{
					Plan:      metadata.PlanName,
					Job:       filepath.Base(metadata.JobFilePath),
					LineIndex: 0, // Not relevant for archived sessions
				})
			}

			projectPath, projectName, worktree, ecosystem := s.parseProjectPath(metadata.WorkingDirectory)

			archivedSessions = append(archivedSessions, SessionInfo{
				SessionID:   metadata.ClaudeSessionID, // Use the native agent ID
				ProjectName: projectName,
				ProjectPath: projectPath,
				Worktree:    worktree,
				Ecosystem:   ecosystem,
				Jobs:        jobInfo,
				LogFilePath: transcriptPath, // Point to the archived transcript
				StartedAt:   metadata.StartedAt,
			})
		}
	}
	return archivedSessions, nil
}
