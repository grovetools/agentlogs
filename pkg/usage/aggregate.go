package usage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// Usage is the four cache-aware token classes plus an extra-total carry. It is
// the shared accumulator across agents, sessions, and projects.
type Usage struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	ExtraTotal   int64 `json:"extra_total"`
}

// Add folds other into u.
func (u *Usage) Add(other Usage) {
	u.Input += other.Input
	u.Output += other.Output
	u.CacheRead += other.CacheRead
	u.CacheWrite5m += other.CacheWrite5m
	u.CacheWrite1h += other.CacheWrite1h
	u.ExtraTotal += other.ExtraTotal
}

// Total is the sum of all token classes (the "totalTokens" figure).
func (u Usage) Total() int64 {
	return u.Input + u.Output + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h + u.ExtraTotal
}

// usageFromTranscript splits a transcript.Usage into the typed Usage, resolving
// the 5m/1h cache-creation breakdown (flat field counts wholly as 5m).
func usageFromTranscript(t transcript.Usage) Usage {
	c5m := t.CacheCreationInputTokens
	c1h := 0
	if t.CacheCreation != nil {
		c5m = t.CacheCreation.Ephemeral5mInputTokens
		c1h = t.CacheCreation.Ephemeral1hInputTokens
	}
	return Usage{
		Input:        int64(t.InputTokens),
		Output:       int64(t.OutputTokens),
		CacheRead:    int64(t.CacheReadInputTokens),
		CacheWrite5m: int64(c5m),
		CacheWrite1h: int64(c1h),
	}
}

// AgentUsage is the usage+cost for one agent (or one model breakdown row).
type AgentUsage struct {
	AgentID        string  `json:"agent_id,omitempty"`
	AgentType      string  `json:"agent_type,omitempty"`
	Model          string  `json:"model,omitempty"`
	Usage          Usage   `json:"usage"`
	CostUSD        float64 `json:"cost_usd"`
	MissingPricing bool    `json:"missing_pricing,omitempty"`
}

// Summary is the aggregated usage+cost for a session (or any grouping), with a
// per-model breakdown and a per-agent breakdown.
type Summary struct {
	SessionID      string       `json:"session_id"`
	ProjectPath    string       `json:"project_path,omitempty"`
	Usage          Usage        `json:"usage"`
	CostUSD        float64      `json:"cost_usd"`
	MissingPricing bool         `json:"missing_pricing,omitempty"`
	Models         []string     `json:"models"`
	ModelBreakdown []AgentUsage `json:"model_breakdown"`
	Agents         []AgentUsage `json:"agents,omitempty"`
	FirstActivity  time.Time    `json:"first_activity"`
	LastActivity   time.Time    `json:"last_activity"`
	MessageCount   int          `json:"message_count"`
}

// dedupe folds entries by the ccusage (message.id, request_id) rule with the
// sidechain fallback, keeping the preferred record on collision. Entries with
// no message.id are never deduped (kept as-is). It returns the surviving
// entries in arrival order.
func dedupe(entries []loadedEntry) []loadedEntry {
	deduped := make([]loadedEntry, 0, len(entries))
	exactKey := make(map[string]int)    // "msgID\x00reqID" -> index
	byMessage := make(map[string][]int) // msgID -> indexes

	for _, e := range entries {
		if e.MessageID == "" {
			deduped = append(deduped, e)
			continue
		}
		ek := e.MessageID + "\x00" + e.RequestID
		idx := -1
		if i, ok := exactKey[ek]; ok {
			idx = i
		} else {
			// Sidechain replay fallback: same message.id where either side is
			// a sidechain entry.
			for _, i := range byMessage[e.MessageID] {
				if e.IsSidechain || deduped[i].IsSidechain {
					idx = i
					break
				}
			}
		}

		if idx >= 0 {
			if shouldReplace(e, deduped[idx]) {
				deduped[idx] = e
			}
			// Keep index maps pointing at the surviving slot.
			exactKey[ek] = idx
			continue
		}

		newIdx := len(deduped)
		deduped = append(deduped, e)
		exactKey[ek] = newIdx
		byMessage[e.MessageID] = append(byMessage[e.MessageID], newIdx)
	}
	return deduped
}

// shouldReplace reports whether candidate should supersede existing on a dedup
// collision: prefer the non-sidechain record, then the higher token total.
// Mirrors ccusage should_replace_deduped_entry (the speed tiebreak is omitted —
// grove transcripts carry no speed marker).
func shouldReplace(candidate, existing loadedEntry) bool {
	if candidate.IsSidechain != existing.IsSidechain {
		return existing.IsSidechain
	}
	ct := usageTokenTotal(candidate.Usage)
	et := usageTokenTotal(existing.Usage)
	if ct != et {
		return ct > et
	}
	return false
}

// summarize builds a Summary from deduped entries grouped under sessionID. It
// computes per-model and per-agent breakdowns (agentID supplied per entry) and
// costs each entry under the given mode/pricing.
func summarize(sessionID, projectPath string, entries []loadedEntry, agentIDs []string, mode CostMode, pm *PricingMap) Summary {
	s := Summary{SessionID: sessionID, ProjectPath: projectPath}
	modelIdx := make(map[string]int)
	agentIdx := make(map[string]int)
	modelsSeen := make(map[string]bool)

	for i, e := range entries {
		eu := usageFromTranscript(e.Usage)
		cost, missing := EntryCost(e.Model, e.Usage, nil, mode, pm)

		s.Usage.Add(eu)
		s.CostUSD += cost
		s.MessageCount++
		if missing != "" {
			s.MissingPricing = true
		}
		if !e.Timestamp.IsZero() {
			if s.FirstActivity.IsZero() || e.Timestamp.Before(s.FirstActivity) {
				s.FirstActivity = e.Timestamp
			}
			if e.Timestamp.After(s.LastActivity) {
				s.LastActivity = e.Timestamp
			}
		}

		if e.Model != "" {
			if !modelsSeen[e.Model] {
				modelsSeen[e.Model] = true
				s.Models = append(s.Models, e.Model)
			}
			mi, ok := modelIdx[e.Model]
			if !ok {
				mi = len(s.ModelBreakdown)
				modelIdx[e.Model] = mi
				s.ModelBreakdown = append(s.ModelBreakdown, AgentUsage{Model: e.Model})
			}
			s.ModelBreakdown[mi].Usage.Add(eu)
			s.ModelBreakdown[mi].CostUSD += cost
			if missing != "" {
				s.ModelBreakdown[mi].MissingPricing = true
			}
		}

		if agentIDs != nil {
			aid := agentIDs[i]
			ai, ok := agentIdx[aid]
			if !ok {
				ai = len(s.Agents)
				agentIdx[aid] = ai
				s.Agents = append(s.Agents, AgentUsage{AgentID: aid, Model: e.Model})
			}
			s.Agents[ai].Usage.Add(eu)
			s.Agents[ai].CostUSD += cost
			if missing != "" {
				s.Agents[ai].MissingPricing = true
			}
		}
	}

	sort.Strings(s.Models)
	sort.Slice(s.ModelBreakdown, func(i, j int) bool {
		return s.ModelBreakdown[i].CostUSD > s.ModelBreakdown[j].CostUSD
	})
	return s
}

// SummarizeTranscript is the single-file primitive: total usage for one
// transcript, deduped within the file. The mode parameter is accepted for
// signature symmetry with SummarizeSession (cost is not computed here — callers
// that need cost use the Summary APIs). Used by the rewired tokens command and
// as the live-tailing primitive in later phases.
func SummarizeTranscript(path string, mode CostMode) (Usage, error) {
	sessionID, projectPath := extractSessionParts(path)
	entries, err := loadFileEntries(path, sessionID, projectPath)
	if err != nil {
		return Usage{}, err
	}
	entries = dedupe(entries)
	var total Usage
	for _, e := range entries {
		total.Add(usageFromTranscript(e.Usage))
	}
	return total, nil
}

// SummarizeSession rolls up a single session by inner-sessionId: parent +
// ad-hoc Task subagents + workflow agents, deduped globally across those files
// and priced. This is the product-facing per-job summary (used by flow Phase
// C). slugDirs are the project-slug dirs to scan; empty scans all of
// ~/.claude/projects.
func SummarizeSession(slugDirs []string, sessionID string, mode CostMode) (Summary, error) {
	pm := DefaultPricing()
	files, err := discoverSessionFiles(slugDirs, sessionID)
	if err != nil {
		return Summary{}, err
	}

	var all []loadedEntry
	var agentIDs []string
	projectPath := ""
	for _, df := range files {
		entries, err := loadFileEntries(df.Path, sessionID, "")
		if err != nil {
			continue
		}
		if projectPath == "" {
			projectPath = slugFromPath(df.Path)
		}
		aid := agentIDFromPath(df.Path, df.Role)
		for _, e := range entries {
			all = append(all, e)
			agentIDs = append(agentIDs, aid)
		}
	}

	// Dedup globally, but preserve the parallel agentID slice.
	all, agentIDs = dedupeWithTags(all, agentIDs)
	s := summarize(sessionID, projectPath, all, agentIDs, mode, pm)
	return s, nil
}

// agentIDFromPath derives a stable agent label from a transcript path: the
// parent transcript is "parent"; agent-*.jsonl files use their "<id>" suffix.
func agentIDFromPath(path, role string) string {
	if role == "parent" {
		return "parent"
	}
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".jsonl")
	base = strings.TrimPrefix(base, "agent-")
	return base
}

// slugFromPath returns the project-slug directory name from a transcript path
// (the directory directly under ~/.claude/projects).
func slugFromPath(path string) string {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// dedupeWithTags applies the same dedup as dedupe but keeps a parallel tag slice
// (e.g. agent IDs) aligned with the surviving entries.
func dedupeWithTags(entries []loadedEntry, tags []string) ([]loadedEntry, []string) {
	deduped := make([]loadedEntry, 0, len(entries))
	dedupedTags := make([]string, 0, len(entries))
	exactKey := make(map[string]int)
	byMessage := make(map[string][]int)

	for i, e := range entries {
		tag := ""
		if i < len(tags) {
			tag = tags[i]
		}
		if e.MessageID == "" {
			deduped = append(deduped, e)
			dedupedTags = append(dedupedTags, tag)
			continue
		}
		ek := e.MessageID + "\x00" + e.RequestID
		idx := -1
		if j, ok := exactKey[ek]; ok {
			idx = j
		} else {
			for _, j := range byMessage[e.MessageID] {
				if e.IsSidechain || deduped[j].IsSidechain {
					idx = j
					break
				}
			}
		}
		if idx >= 0 {
			if shouldReplace(e, deduped[idx]) {
				deduped[idx] = e
				dedupedTags[idx] = tag
			}
			exactKey[ek] = idx
			continue
		}
		newIdx := len(deduped)
		deduped = append(deduped, e)
		dedupedTags = append(dedupedTags, tag)
		exactKey[ek] = newIdx
		byMessage[e.MessageID] = append(byMessage[e.MessageID], newIdx)
	}
	return deduped, dedupedTags
}

// ScanResult is the full per-session rollup over a set of project slugs, plus
// the grand total. It mirrors ccusage's session report shape and is the basis
// for the acceptance gate.
type ScanResult struct {
	Sessions []Summary
	Totals   Summary
}

// ScanProjects reads every transcript under the given project-slug dirs (or all
// of ~/.claude/projects when slugDirs is empty), dedups globally, groups by the
// ccusage path-derived session id, prices each session, and returns per-session
// summaries plus a grand total. since, when non-zero, drops entries older than
// it.
//
// Grouping uses ccusage's rules (extractSessionParts), so each root agent-*.jsonl
// is its own "agent-<id>" session and workflow agents roll up to their session-id
// directory — this is what makes ScanProjects diff-match `ccusage claude session`.
func ScanProjects(slugDirs []string, mode CostMode, since time.Time) (ScanResult, error) {
	pm := DefaultPricing()

	if len(slugDirs) == 0 {
		root, err := claudeProjectsDir()
		if err != nil {
			return ScanResult{}, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return ScanResult{}, err
		}
		for _, e := range entries {
			if e.IsDir() {
				slugDirs = append(slugDirs, filepath.Join(root, e.Name()))
			}
		}
	}

	var all []loadedEntry
	for _, slugDir := range slugDirs {
		err := filepath.Walk(slugDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if filepath.Base(path) == "journal.jsonl" {
				return nil
			}
			sessionID, projectPath := extractSessionParts(path)
			fileEntries, err := loadFileEntries(path, sessionID, projectPath)
			if err != nil {
				return nil
			}
			all = append(all, fileEntries...)
			return nil
		})
		if err != nil {
			return ScanResult{}, err
		}
	}

	if !since.IsZero() {
		filtered := all[:0]
		for _, e := range all {
			if e.Timestamp.IsZero() || !e.Timestamp.Before(since) {
				filtered = append(filtered, e)
			}
		}
		all = filtered
	}

	all = dedupe(all)

	// Group by the (projectPath, sessionId) composite — ccusage's session key.
	// The same path-derived session id (e.g. a workflow run wf_*) can appear
	// under multiple parent-session project paths and is reported as separate
	// rows, so sessionId alone is not unique.
	type groupKey struct {
		project string
		session string
	}
	bySession := make(map[groupKey][]loadedEntry)
	order := make([]groupKey, 0)
	for _, e := range all {
		k := groupKey{project: e.ProjectPath, session: e.SessionID}
		if _, ok := bySession[k]; !ok {
			order = append(order, k)
		}
		bySession[k] = append(bySession[k], e)
	}

	result := ScanResult{}
	for _, k := range order {
		group := bySession[k]
		s := summarize(k.session, k.project, group, nil, mode, pm)
		// ccusage drops sessions with no billable tokens; mirror that so the
		// per-session report diff-matches.
		if s.Usage.Total() == 0 {
			continue
		}
		result.Sessions = append(result.Sessions, s)
	}

	// Grand total over every deduped entry.
	result.Totals = summarize("", "", all, nil, mode, pm)
	result.Totals.SessionID = ""

	sort.Slice(result.Sessions, func(i, j int) bool {
		return result.Sessions[i].LastActivity.Before(result.Sessions[j].LastActivity)
	})
	return result, nil
}
