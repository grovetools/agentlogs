package usage

import (
	"os"
	"sync"
)

// usageFileCache makes live session summaries incremental. Claude transcripts
// are append-only JSONL, so a growing file only requires decoding bytes after
// the last complete newline. Truncation/replacement falls back to a full parse.
type usageFileCacheEntry struct {
	mu       sync.Mutex
	size     int64
	modTime  int64
	complete bool
	entries  []loadedEntry
}

var usageFileCache sync.Map // path -> *usageFileCacheEntry

func loadFileEntries(path, sessionID, projectPath string) ([]loadedEntry, error) {
	value, _ := usageFileCache.LoadOrStore(path, &usageFileCacheEntry{})
	cached := value.(*usageFileCacheEntry)
	cached.mu.Lock()
	defer cached.mu.Unlock()

	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if cached.size == stat.Size() && cached.modTime == stat.ModTime().UnixNano() {
		return cloneLoadedEntries(cached.entries, sessionID, projectPath), nil
	}

	offset := int64(0)
	base := []loadedEntry(nil)
	if cached.complete && cached.size > 0 && stat.Size() > cached.size {
		offset = cached.size
		base = cached.entries
	}
	added, err := loadFileEntriesFrom(path, sessionID, projectPath, offset)
	if err != nil {
		return nil, err
	}
	all := make([]loadedEntry, 0, len(base)+len(added))
	all = append(all, base...)
	all = append(all, added...)

	// Cache only a stable snapshot. If a writer raced us, the next call will
	// parse again rather than publishing an ambiguous byte offset.
	after, statErr := os.Stat(path)
	if statErr == nil && after.Size() == stat.Size() && after.ModTime().UnixNano() == stat.ModTime().UnixNano() {
		cached.size = stat.Size()
		cached.modTime = stat.ModTime().UnixNano()
		cached.complete = fileEndsWithNewline(path, stat.Size())
		cached.entries = all
	}
	return cloneLoadedEntries(all, sessionID, projectPath), nil
}

func cloneLoadedEntries(entries []loadedEntry, sessionID, projectPath string) []loadedEntry {
	out := append([]loadedEntry(nil), entries...)
	// Cached entries can be reused by callers with different path-derived tags.
	for i := range out {
		out[i].SessionID = sessionID
		out[i].ProjectPath = projectPath
	}
	return out
}

func fileEndsWithNewline(path string, size int64) bool {
	if size == 0 {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(size-1, 0); err != nil {
		return false
	}
	var last [1]byte
	_, err = f.Read(last[:])
	return err == nil && last[0] == '\n'
}

func resetUsageFileCache() {
	usageFileCache = sync.Map{}
}
