package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
			// The artifact directory is ground truth, and reaching it needs
			// nothing but plan_dir + job id. Gating that on job.Type made
			// resolution depend on record shape: a typeless duplicate record
			// for the same job took the else branch below and answered with a
			// job.log path (orchestrator output) or with nothing at all, while
			// the transcript sat in the artifact directory the type gate had
			// skipped. Try the artifact first for every record.
			if info := resolveFlowArtifactSession(job); info != nil {
				return info, nil
			}
			// Several daemon records can name the same job (a Flow-ID record
			// and a legacy filename-keyed one). Whichever won the lookup, the
			// record that resolves to a real transcript is the right answer.
			if info := resolveSiblingArtifactSession(daemonClient, job); info != nil {
				return info, nil
			}
			if !isAgentJobType(job.Type) {
				// Non-agent jobs have no provider transcript; their daemon
				// record is the whole answer.
				return jobInfoToSessionInfo(job), nil
			}
			// The daemon log is orchestrator output, not the provider transcript.
			// Fall through for legacy jobs whose artifact has no session file.
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
				// Enrich so file-based providers can actually open the
				// transcript: the job's artifact dir first, scanner last.
				enrichLogFilePath(info, artifactSessionHints{
					planDirs: []string{session.PlanDirectory, filepath.Dir(session.JobFilePath)},
					jobIDs:   []string{session.ID, spec, session.ClaudeSessionID},
				})
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

// artifactSessionHints carry the plan/job identity the daemon reported for a
// session. They let the transcript be resolved from that job's own artifact
// directory instead of a full multi-provider corpus scan. Both fields are
// candidate lists because the daemon exposes the plan directory and the job id
// under several names depending on how the session was registered; empty and
// duplicate values are ignored.
type artifactSessionHints struct {
	planDirs []string
	jobIDs   []string
}

// resolve tries every (planDir, jobID) pair, returning the first artifact-owned
// transcript path found.
func (h artifactSessionHints) resolve() string {
	seen := make(map[string]bool, len(h.planDirs)*len(h.jobIDs))
	for _, planDir := range h.planDirs {
		if planDir == "" || planDir == "." {
			continue
		}
		for _, jobID := range h.jobIDs {
			if jobID == "" {
				continue
			}
			key := planDir + "\x00" + jobID
			if seen[key] {
				continue
			}
			seen[key] = true
			if resolved, _ := resolveArtifactSessionDir(planDir, jobID); resolved != nil && resolved.LogFilePath != "" {
				return resolved.LogFilePath
			}
		}
	}
	return ""
}

// enrichLogFilePath populates info.LogFilePath when the daemon resolved a
// session but didn't include the transcript path. The job's own artifact
// directory is checked first; only if that fails does this fall back to a full
// scanner pass, matching first by SessionID and then by (Plan, Job) pair.
func enrichLogFilePath(info *SessionInfo, hints artifactSessionHints) {
	if info == nil || info.LogFilePath != "" {
		return
	}
	// Resolve the exact job artifact before paying for a corpus-wide scan.
	if path := hints.resolve(); path != "" {
		info.LogFilePath = path
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

// isAgentJobType reports whether a daemon job record describes an agent run,
// whose transcript is written by a provider rather than by the orchestrator.
func isAgentJobType(t models.JobType) bool {
	return t == "interactive_agent" || t == "headless_agent" || t == "isolated_agent"
}

// resolveSiblingArtifactSession looks for another daemon record describing the
// same job (same plan directory and job file) whose artifact directory does
// hold a transcript.
//
// Duplicate records exist because job submission once minted a second,
// filename-derived key for a job that already had a Flow ID. The duplicate
// carries no type and points at job.log, so when it wins the lookup the real
// transcript — sitting under the Flow ID's artifact directory — is never
// consulted. Records are being collapsed, but resolution must stay correct for
// the ones already on disk.
func resolveSiblingArtifactSession(client daemon.Client, job *models.JobInfo) *SessionInfo {
	if client == nil || job == nil || job.PlanDir == "" || job.JobFile == "" {
		return nil
	}
	siblings, err := client.ListJobs(context.Background(), models.JobFilter{})
	if err != nil {
		return nil
	}
	for _, sibling := range siblings {
		if sibling == nil || sibling.ID == job.ID {
			continue
		}
		if sibling.JobFile != job.JobFile || filepath.Clean(sibling.PlanDir) != filepath.Clean(job.PlanDir) {
			continue
		}
		if info := resolveFlowArtifactSession(sibling); info != nil {
			return info
		}
	}
	return nil
}

// resolveFlowArtifactSession resolves the transcript owned by one daemon job
// without scanning provider-global storage.
func resolveFlowArtifactSession(job *models.JobInfo) *SessionInfo {
	if job == nil {
		return nil
	}
	resolved, archived := resolveArtifactSessionDir(job.PlanDir, job.ID)
	if resolved == nil {
		return nil
	}
	// Archived sessions are already fully described by their own metadata.
	if archived {
		return resolved
	}
	info := jobInfoToSessionInfo(job)
	info.SessionID = resolved.SessionID
	info.LogFilePath = resolved.LogFilePath
	info.Provider = resolved.Provider
	info.StartedAt = resolved.StartedAt
	if resolved.ProjectPath != "" {
		info.ProjectPath = resolved.ProjectPath
		info.ProjectName = resolved.ProjectName
		info.Worktree = resolved.Worktree
		info.Ecosystem = resolved.Ecosystem
	}
	if len(resolved.Jobs) > 0 {
		info.Jobs = resolved.Jobs
	}
	return info
}

// resolveArtifactSessionDir resolves the transcript stored in one flow job's
// .artifacts/<jobID> directory, without touching provider-global storage.
// Completed/legacy jobs have metadata.json + transcript.jsonl, in which case
// archived is true and the returned SessionInfo is fully populated from that
// metadata. New Pi jobs instead write sessions/*.jsonl, for which only the
// transcript-derived fields are set and the caller merges them onto its own
// record.
func resolveArtifactSessionDir(planDir, jobID string) (info *SessionInfo, archived bool) {
	if planDir == "" || jobID == "" {
		return nil, false
	}
	artifactDir := filepath.Join(planDir, ".artifacts", jobID)
	scanner := NewScannerWithoutDaemon()
	for _, s := range scanner.scanArchivedSessionsInPlanDir(planDir) {
		if s.LogFilePath == filepath.Join(artifactDir, "transcript.jsonl") ||
			strings.Contains(s.LogFilePath, artifactDir+string(filepath.Separator)) {
			found := s
			return &found, true
		}
	}

	matches, _ := filepath.Glob(filepath.Join(artifactDir, "sessions", "*.jsonl"))
	var newest string
	var newestMod time.Time
	for _, path := range matches {
		stat, err := os.Stat(path)
		if err == nil && !stat.IsDir() && (newest == "" || stat.ModTime().After(newestMod)) {
			newest, newestMod = path, stat.ModTime()
		}
	}
	if newest == "" {
		return nil, false
	}

	sessionID, cwd, startedAt, jobs, found := scanner.parsePiLog(newest)
	if !found {
		return nil, false
	}
	out := &SessionInfo{
		SessionID:   sessionID,
		LogFilePath: newest,
		Provider:    "pi",
		StartedAt:   startedAt,
		Jobs:        jobs,
	}
	if cwd != "" {
		// Derive worktree/ecosystem the same way the scanner path does, so
		// fast-path sessions don't show blank columns in flow's status TUI.
		out.ProjectPath, out.ProjectName, out.Worktree, out.Ecosystem = scanner.parseProjectPath(cwd)
	}
	return out, false
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
