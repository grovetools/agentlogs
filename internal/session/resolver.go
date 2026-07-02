package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

// ResolveSessionInfo finds a session's metadata based on a specifier which can be a
// plan/job string, a session ID, or a direct file path to a job file or log file.
// It prioritizes the fastest lookup methods first.
func ResolveSessionInfo(spec string) (*SessionInfo, error) {
	// Try daemon lookup first (fastest path)
	daemonClient := daemon.NewWithAutoStart()
	defer daemonClient.Close()

	if daemonClient.IsRunning() {
		// Try daemon job registry first — this is the primary source in the new architecture
		if job, err := daemonClient.GetJob(context.Background(), spec); err == nil && job != nil {
			if job.Type == "interactive_agent" || job.Type == "headless_agent" || job.Type == "isolated_agent" {
				// For agent jobs, the true transcript is the provider's JSONL file.
				// The daemon only has orchestrator launch output, not the actual transcript.
				// Fall through to full scan so it matches via the session registry with LogFilePath.
			} else {
				return jobInfoToSessionInfo(job), nil
			}
		} else {
			// Fall back to daemon session lookup (for sessions not managed as jobs)
			if session, err := daemonClient.GetSession(context.Background(), spec); err == nil && session != nil {
				// Found via daemon - convert to SessionInfo
				var jobs []JobInfo
				if session.PlanName != "" && session.JobFilePath != "" {
					jobs = append(jobs, JobInfo{
						Plan: session.PlanName,
						Job:  filepath.Base(session.JobFilePath),
					})
				}
				info := &SessionInfo{
					SessionID:   session.ID,
					ProjectName: filepath.Base(session.WorkingDirectory),
					ProjectPath: session.WorkingDirectory,
					Jobs:        jobs,
					StartedAt:   session.StartedAt,
					Provider:    session.Provider,
					Status:      session.Status,
					PID:         session.PID,
				}
				// Daemon records don't carry LogFilePath. For opencode,
				// follow the transcript pointer recorded in the session
				// registry (native_session_id + opencode_storage_root)
				// before resorting to a full scanner pass.
				if session.Provider == "opencode" {
					if p := resolveOpenCodePointer(session.ID); p != nil {
						info.SessionID = p.SessionID
						info.LogFilePath = p.LogFilePath
					}
				}
				// Enrich from scanner so file-based providers can actually
				// open the transcript.
				enrichLogFilePath(info)
				return info, nil
			}
		}
	}

	// Before the full scan, try the opencode transcript pointer: the grove
	// opencode plugin records native_session_id + opencode_storage_root in
	// the hooks session registry, so opencode specs (flow job id, native
	// ses_* id, or plan/job) resolve without walking every provider's
	// storage.
	if info := resolveOpenCodePointer(spec); info != nil {
		return info, nil
	}

	// Fall back to full scan
	scanner := NewScanner()
	allSessions, err := scanner.Scan()
	if err != nil {
		return nil, fmt.Errorf("failed to scan for sessions: %w", err)
	}
	if len(allSessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}

	// Sort sessions by started time, most recent first
	// This ensures we match the most recent session when multiple sessions have the same job
	sort.Slice(allSessions, func(i, j int) bool {
		return allSessions[i].StartedAt.After(allSessions[j].StartedAt)
	})

	// Strategy 1: Check if spec is a direct log file path
	absSpec, err := filepath.Abs(spec)
	if err == nil {
		for i, s := range allSessions {
			if s.LogFilePath == absSpec {
				return &allSessions[i], nil
			}
		}
	}

	// Strategy 2: Check for session ID or plan/job spec.
	// When multiple sessions match (e.g. a filesystem-backed entry and a
	// daemon-only entry for the same job), prefer the one with LogFilePath
	// set; otherwise fall back to the first match so callers still get a hit.
	parts := strings.Split(spec, "/")
	isPlanJobSpec := len(parts) == 2 && strings.HasSuffix(parts[1], ".md")

	fallbackIdx := -1
	for i, s := range allSessions {
		matched := false
		if s.SessionID == spec {
			matched = true
		} else if isPlanJobSpec {
			planName := parts[0]
			jobName := parts[1]
			for _, job := range s.Jobs {
				if job.Plan == planName && job.Job == jobName {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		if s.LogFilePath != "" {
			return &allSessions[i], nil
		}
		if fallbackIdx == -1 {
			fallbackIdx = i
		}
	}
	if fallbackIdx != -1 {
		return &allSessions[fallbackIdx], nil
	}

	// Strategy 3: Check if spec is a job file path (which might not be part of a plan/job spec)
	if _, err := os.Stat(spec); err == nil {
		jobFilename := filepath.Base(spec)
		planName := filepath.Base(filepath.Dir(spec))
		fsFallbackIdx := -1
		for i, s := range allSessions {
			matched := false
			for _, job := range s.Jobs {
				if job.Plan == planName && job.Job == jobFilename {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if s.LogFilePath != "" {
				return &allSessions[i], nil
			}
			if fsFallbackIdx == -1 {
				fsFallbackIdx = i
			}
		}
		if fsFallbackIdx != -1 {
			return &allSessions[fsFallbackIdx], nil
		}
	}

	return nil, fmt.Errorf("could not find session matching spec: %s", spec)
}

// enrichLogFilePath populates info.LogFilePath from a local scanner pass when
// the daemon resolved a session but didn't include the transcript path.
// Matches first by SessionID, then by (Plan, Job) pair across discovered sessions.
func enrichLogFilePath(info *SessionInfo) {
	if info == nil || info.LogFilePath != "" {
		return
	}
	scanner := NewScannerWithoutDaemon()
	allSessions, err := scanner.Scan()
	if err != nil {
		return
	}
	for _, s := range allSessions {
		if s.LogFilePath == "" {
			continue
		}
		if s.SessionID == info.SessionID {
			info.LogFilePath = s.LogFilePath
			return
		}
		for _, job := range s.Jobs {
			for _, target := range info.Jobs {
				if job.Plan == target.Plan && job.Job == target.Job {
					info.LogFilePath = s.LogFilePath
					return
				}
			}
		}
	}
}

// jobInfoToSessionInfo converts a daemon JobInfo into a SessionInfo.
func jobInfoToSessionInfo(job *models.JobInfo) *SessionInfo {
	var jobs []JobInfo
	if job.JobFile != "" {
		jobs = append(jobs, JobInfo{
			Plan: filepath.Base(job.PlanDir),
			Job:  job.JobFile,
		})
	}

	startedAt := job.SubmittedAt
	if job.StartedAt != nil {
		startedAt = *job.StartedAt
	}

	return &SessionInfo{
		SessionID:   job.ID,
		ProjectName: filepath.Base(job.PlanDir),
		ProjectPath: job.PlanDir,
		Jobs:        jobs,
		StartedAt:   startedAt,
		Status:      job.Status,
		Provider:    "claude", // Default; daemon jobs are typically Claude agents
	}
}
