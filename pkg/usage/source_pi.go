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

// piUsageSource scans pi session transcripts under the munged-cwd
// ~/.pi/agent/sessions/--<cwd>--/ layout (transcript.PiSessionsGlob is the
// single definition of that layout).
type piUsageSource struct{}

func (piUsageSource) Provider() string { return "pi" }

// CollectEntries loads per-message usage entries from every pi session file.
// A missing ~/.pi/agent/sessions store yields (nil, nil).
func (piUsageSource) CollectEntries() ([]loadedEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(transcript.PiSessionsGlob(homeDir, ""))
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	var all []loadedEntry
	for _, path := range matches {
		entries, err := piTranscriptEntries(path)
		if err != nil {
			continue
		}
		all = append(all, entries...)
	}
	return all, nil
}

// piSessionLine is the subset of a pi session JSONL line the usage loader
// needs: the session header identity plus assistant messages' usage. pi's
// per-message usage.cost.total is a provider-computed dollar figure and is
// ingested as the entry's native cost — no pricing-table lookup happens for
// pi (EntryCost gives native cost precedence).
type piSessionLine struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"` // session header only
	Message   *struct {
		Role  string `json:"role"`
		Model string `json:"model"`
		Usage *struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Cost       struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
	} `json:"message"`
}

// piTranscriptEntries loads the usage-bearing entries of one pi session file:
// every assistant message carrying usage, in file order. Unlike transcript
// RENDERING — which must linearize pi's entry tree along the active branch
// (transcript.NormalizePiFile) — usage accounting deliberately counts every
// branch: abandoned/retried branches were real, billed API calls. Entry ids
// are unique per line (retries append new ids), so the shared dedup pass is a
// no-op for pi. The session id comes from the header line, falling back to
// the filename (<timestamp>_<uuid>.jsonl); the project path is the header's
// cwd, falling back to the munged per-cwd directory name.
func piTranscriptEntries(path string) ([]loadedEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(sessionID, "_"); i >= 0 && i+1 < len(sessionID) {
		sessionID = sessionID[i+1:]
	}
	projectPath := filepath.Base(filepath.Dir(path))

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	var entries []loadedEntry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap prefilter: header + usage-bearing messages only.
		s := string(line)
		if !strings.Contains(s, "\"usage\"") && !strings.Contains(s, "\"session\"") {
			continue
		}
		var raw piSessionLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		switch raw.Type {
		case "session":
			if raw.ID != "" {
				sessionID = raw.ID
			}
			if raw.Cwd != "" {
				projectPath = raw.Cwd
			}
			continue
		case "message":
		default:
			continue
		}
		if raw.Message == nil || raw.Message.Role != "assistant" || raw.Message.Usage == nil {
			continue
		}
		u := raw.Message.Usage
		var ts time.Time
		if raw.Timestamp != "" {
			ts, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
		}
		cost := u.Cost.Total
		entries = append(entries, loadedEntry{
			SessionID:   sessionID,
			ProjectPath: projectPath,
			MessageID:   raw.ID,
			Model:       raw.Message.Model,
			Timestamp:   ts,
			Usage: transcript.Usage{
				InputTokens:              u.Input,
				OutputTokens:             u.Output,
				CacheReadInputTokens:     u.CacheRead,
				CacheCreationInputTokens: u.CacheWrite,
			},
			Provider: "pi",
			CostUSD:  &cost,
		})
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	// Backfill session identity discovered mid-file onto earlier entries
	// (the header is line one in practice; this is defensive).
	for i := range entries {
		entries[i].SessionID = sessionID
		entries[i].ProjectPath = projectPath
	}
	return entries, nil
}
