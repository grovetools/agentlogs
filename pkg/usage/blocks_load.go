package usage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BlockReport pairs an identified block with derived live metrics. Burn and
// Projection are populated only for the active block (nil otherwise), mirroring
// ccusage's block_json which computes them only when is_active.
type BlockReport struct {
	Block      Block       `json:"block"`
	Burn       *BurnRate   `json:"burn_rate,omitempty"`
	Projection *Projection `json:"projection,omitempty"`
}

// loadBlockEntries reads, dedups, and prices every usage-bearing entry across
// the given transcript files, returning them as block entries ordered by
// timestamp. Entries without a timestamp are dropped (the block algorithm is
// timestamp-driven). Files that fail to open are skipped.
func loadBlockEntries(paths []string, mode CostMode, pm *PricingMap) []blockEntry {
	var all []loadedEntry
	for _, p := range paths {
		sessionID, projectPath := extractSessionParts(p)
		fileEntries, err := loadFileEntries(p, sessionID, projectPath)
		if err != nil {
			continue
		}
		all = append(all, fileEntries...)
	}
	return blockEntriesFromLoaded(all, mode, pm)
}

// blockEntriesFromLoaded dedups and prices already-loaded entries into
// timestamp-ordered block entries. Claude entries carry no native cost
// (CostUSD nil), so their pricing is the historical calculate path; pi and
// opencode entries use their provider-native cost (see EntryCost).
func blockEntriesFromLoaded(all []loadedEntry, mode CostMode, pm *PricingMap) []blockEntry {
	all = dedupe(all)

	entries := make([]blockEntry, 0, len(all))
	for _, e := range all {
		if e.Timestamp.IsZero() {
			continue
		}
		cost, _ := EntryCost(e.Model, e.Usage, e.CostUSD, mode, pm)
		entries = append(entries, blockEntry{
			Timestamp: e.Timestamp,
			Usage:     usageFromTranscript(e.Usage),
			Model:     e.Model,
			CostUSD:   cost,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	return entries
}

// blockEntriesWithin returns the subset of sorted entries that fall inside
// [start, end). Used to recover a block's own entries for burn-rate/projection
// math without threading the entry slices through the Block type.
func blockEntriesWithin(entries []blockEntry, start, end time.Time) []blockEntry {
	var out []blockEntry
	for _, e := range entries {
		if !e.Timestamp.Before(start) && e.Timestamp.Before(end) {
			out = append(out, e)
		}
	}
	return out
}

// BuildBlockReports identifies blocks over the entries and attaches burn rate
// and projection to the active block. duration is the rolling window length;
// now is the active-block reference time.
func BuildBlockReports(entries []blockEntry, duration time.Duration, now time.Time) []BlockReport {
	blocks := IdentifySessionBlocks(entries, duration, now)
	reports := make([]BlockReport, 0, len(blocks))
	for _, b := range blocks {
		r := BlockReport{Block: b}
		if b.IsActive {
			be := blockEntriesWithin(entries, b.StartTime, b.EndTime)
			r.Burn = CalculateBurnRate(b, be)
			r.Projection = ProjectBlockUsage(b, be, now)
		}
		reports = append(reports, r)
	}
	return reports
}

// SessionBlocks rolls up a single session's transcripts (parent + ad-hoc Task
// subagents + workflow agents, discovered by inner session id) into rolling
// usage blocks with burn-rate and projection on the active block. slugDirs are
// the project-slug dirs to scan; empty scans all of ~/.claude/projects.
func SessionBlocks(slugDirs []string, sessionID string, mode CostMode, duration time.Duration) ([]BlockReport, error) {
	pm := DefaultPricing()
	files, err := discoverSessionFiles(slugDirs, sessionID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	entries := loadBlockEntries(paths, mode, pm)
	return BuildBlockReports(entries, duration, time.Now()), nil
}

// ProjectBlocks rolls up every transcript under the given project-slug dirs (or
// all of ~/.claude/projects when slugDirs is empty) into rolling usage blocks.
// This is the global "what's my current 5-hour-block burn" view used by
// `aglogs usage --blocks` and the live watcher.
func ProjectBlocks(slugDirs []string, mode CostMode, duration time.Duration) ([]BlockReport, error) {
	pm := DefaultPricing()

	if len(slugDirs) == 0 {
		root, err := claudeProjectsDir()
		if err != nil {
			return nil, err
		}
		dirEntries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range dirEntries {
			if e.IsDir() {
				slugDirs = append(slugDirs, filepath.Join(root, e.Name()))
			}
		}
	}

	var paths []string
	for _, slugDir := range slugDirs {
		_ = filepath.Walk(slugDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if filepath.Base(path) == "journal.jsonl" {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
	}

	entries := loadBlockEntries(paths, mode, pm)
	return BuildBlockReports(entries, duration, time.Now()), nil
}
