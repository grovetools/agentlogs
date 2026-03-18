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
	daemonClient := daemon.New()
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
				return &SessionInfo{
					SessionID:   session.ID,
					ProjectName: filepath.Base(session.WorkingDirectory),
					ProjectPath: session.WorkingDirectory,
					Jobs:        jobs,
					StartedAt:   session.StartedAt,
					Provider:    session.Provider,
					Status:      session.Status,
					PID:         session.PID,
				}, nil
			}
		}
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

	// Strategy 2: Check for session ID or plan/job spec
	parts := strings.Split(spec, "/")
	isPlanJobSpec := len(parts) == 2 && strings.HasSuffix(parts[1], ".md")

	for i, s := range allSessions {
		// Match by session ID
		if s.SessionID == spec {
			return &allSessions[i], nil
		}

		// Match by plan/job spec
		if isPlanJobSpec {
			planName := parts[0]
			jobName := parts[1]
			for _, job := range s.Jobs {
				if job.Plan == planName && job.Job == jobName {
					return &allSessions[i], nil
				}
			}
		}
	}

	// Strategy 3: Check if spec is a job file path (which might not be part of a plan/job spec)
	if _, err := os.Stat(spec); err == nil {
		jobFilename := filepath.Base(spec)
		planName := filepath.Base(filepath.Dir(spec))
		for i, s := range allSessions {
			for _, job := range s.Jobs {
				if job.Plan == planName && job.Job == jobFilename {
					return &allSessions[i], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("could not find session matching spec: %s", spec)
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
