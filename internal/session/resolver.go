package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveSessionInfo finds a session's metadata based on a specifier which can be a
// plan/job string, a session ID, or a direct file path to a job file or log file.
// It prioritizes the fastest lookup methods first.
func ResolveSessionInfo(spec string) (*SessionInfo, error) {
	scanner := NewScanner()
	allSessions, err := scanner.Scan()
	if err != nil {
		return nil, fmt.Errorf("failed to scan for sessions: %w", err)
	}
	if len(allSessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}

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
