package session

import (
	"os"
	"sync"
	"time"
)

// transcriptParseCache avoids repeatedly decoding the immutable prefix of every
// transcript during global scans. Session discovery only needs the first 100
// lines, so size+mtime is a sufficient invalidation key for this metadata
// cache. Values are copied on return because callers may retain or modify Jobs.
type transcriptParseResult struct {
	sessionID string
	cwd       string
	startedAt time.Time
	jobs      []JobInfo
	found     bool
}

type transcriptParseCacheEntry struct {
	size    int64
	modTime int64
	result  transcriptParseResult
}

var transcriptParseCache = struct {
	sync.RWMutex
	entries map[string]transcriptParseCacheEntry
}{entries: make(map[string]transcriptParseCacheEntry)}

func (s *Scanner) parseTranscriptCached(path string) (transcriptParseResult, bool) {
	stat, err := os.Stat(path)
	if err != nil {
		return transcriptParseResult{}, false
	}

	transcriptParseCache.RLock()
	cached, ok := transcriptParseCache.entries[path]
	transcriptParseCache.RUnlock()
	if ok && cached.size == stat.Size() && cached.modTime == stat.ModTime().UnixNano() {
		cached.result.jobs = append([]JobInfo(nil), cached.result.jobs...)
		return cached.result, true
	}

	var result transcriptParseResult
	switch providerFromTranscriptPath(path) {
	case "codex":
		result.sessionID, result.cwd, result.startedAt, result.jobs, result.found = s.parseCodexLog(path)
	case "pi":
		result.sessionID, result.cwd, result.startedAt, result.jobs, result.found = s.parsePiLog(path)
	default:
		result.sessionID, result.cwd, result.startedAt, result.jobs, result.found = s.parseClaudeLog(path)
	}

	// Do not publish a result if the file changed while it was being read.
	if after, err := os.Stat(path); err == nil && after.Size() == stat.Size() && after.ModTime().UnixNano() == stat.ModTime().UnixNano() {
		stored := result
		stored.jobs = append([]JobInfo(nil), result.jobs...)
		transcriptParseCache.Lock()
		transcriptParseCache.entries[path] = transcriptParseCacheEntry{size: stat.Size(), modTime: stat.ModTime().UnixNano(), result: stored}
		transcriptParseCache.Unlock()
	}
	return result, false
}

func resetTranscriptParseCache() {
	transcriptParseCache.Lock()
	transcriptParseCache.entries = make(map[string]transcriptParseCacheEntry)
	transcriptParseCache.Unlock()
}
