package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// codexUsageSource scans codex rollout transcripts under the nested
// ~/.codex/sessions/YYYY/MM/DD layout (transcript.CodexSessionsGlob is the
// single definition of that layout).
type codexUsageSource struct{}

func (codexUsageSource) Provider() string { return "codex" }

// CollectEntries loads per-turn usage entries from every codex rollout file.
// A missing ~/.codex/sessions store yields (nil, nil).
func (codexUsageSource) CollectEntries() ([]loadedEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(transcript.CodexSessionsGlob(homeDir, ""))
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	var all []loadedEntry
	for _, path := range matches {
		entries, err := codexTranscriptEntries(path)
		if err != nil {
			continue // unreadable file: skip, like the Claude walker does
		}
		all = append(all, entries...)
	}
	return all, nil
}

// codexRolloutLine is the envelope subset the usage loader needs from one
// codex rollout line: the wall-clock timestamp plus the payload fields of
// session_meta (session identity) and turn_context (model in effect).
type codexRolloutLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID    string `json:"id"`    // session_meta: native session id
		Cwd   string `json:"cwd"`   // session_meta: working directory
		Model string `json:"model"` // turn_context: model for following turns
	} `json:"payload"`
}

// codexTranscriptEntries loads the usage-bearing entries of one codex rollout
// file. Codex reports usage as cumulative token_count events (one per turn),
// so each entry's usage is the DELTA of total_token_usage between consecutive
// events — deltas sum exactly to the final cumulative total even if an event
// duplicates or repeats a turn. A negative delta (defensive; totals are
// monotonic in codex) falls back to that event's last_token_usage. The model
// comes from the most recent turn_context line; a file that never names one
// gets "codex/unknown" so real usage is flagged unpriced instead of silently
// costing $0. MessageID stays empty — codex entries never dedupe across
// files. Session id prefers session_meta payload.id, falling back to the
// rollout filename's trailing uuid.
func codexTranscriptEntries(path string) ([]loadedEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := codexSessionIDFromFilename(path)
	projectPath := ""
	model := ""

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	var entries []loadedEntry
	var prevTotal transcript.UnifiedTokens
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap prefilter: only envelope types the loader consumes.
		s := string(line)
		if !strings.Contains(s, "token_count") &&
			!strings.Contains(s, "session_meta") &&
			!strings.Contains(s, "turn_context") {
			continue
		}

		var raw codexRolloutLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		switch raw.Type {
		case "session_meta":
			if raw.Payload.ID != "" {
				sessionID = raw.Payload.ID
			}
			if raw.Payload.Cwd != "" {
				projectPath = raw.Payload.Cwd
			}
			continue
		case "turn_context":
			if raw.Payload.Model != "" {
				model = raw.Payload.Model
			}
			continue
		}

		tc, ok := transcript.ParseCodexTokenCountLine(line)
		if !ok {
			continue
		}
		delta := codexUsageDelta(prevTotal, tc)
		prevTotal = tc.Total

		var ts time.Time
		if raw.Timestamp != "" {
			ts, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
		}
		entryModel := model
		if entryModel == "" {
			entryModel = "codex/unknown"
		}
		entries = append(entries, loadedEntry{
			SessionID:   sessionID,
			ProjectPath: projectPath,
			Model:       entryModel,
			Timestamp:   ts,
			Usage: transcript.Usage{
				InputTokens:          delta.Input,
				OutputTokens:         delta.Output,
				CacheReadInputTokens: delta.CacheRead,
			},
			Provider: "codex",
		})
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	if projectPath == "" {
		projectPath = "codex"
	}
	for i := range entries {
		entries[i].ProjectPath = projectPath
	}
	return entries, nil
}

// codexUsageDelta computes the per-event usage as the difference of cumulative
// totals, falling back to the event's own last-turn usage when any class went
// backwards (a reset or malformed event).
func codexUsageDelta(prev transcript.UnifiedTokens, tc transcript.CodexTokenCount) transcript.UnifiedTokens {
	d := transcript.UnifiedTokens{
		Input:     tc.Total.Input - prev.Input,
		Output:    tc.Total.Output - prev.Output,
		CacheRead: tc.Total.CacheRead - prev.CacheRead,
	}
	if d.Input < 0 || d.Output < 0 || d.CacheRead < 0 {
		return tc.Last
	}
	return d
}

// codexSessionIDFromFilename extracts the trailing uuid of a codex rollout
// filename (rollout-<timestamp>-<uuid>.jsonl), falling back to the whole base
// name without extension.
func codexSessionIDFromFilename(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	// The uuid is the last 5 dash-separated groups (8-4-4-4-12 hex).
	parts := strings.Split(base, "-")
	if len(parts) >= 5 {
		candidate := strings.Join(parts[len(parts)-5:], "-")
		if len(candidate) == 36 {
			return candidate
		}
	}
	return base
}
