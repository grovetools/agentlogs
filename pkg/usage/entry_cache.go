package usage

import (
	"crypto/sha256"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

// Cache bounds. The cache exists to make repeated summaries of *live* sessions
// cheap, so the working set it must cover is "transcripts a daemon is actively
// watching", not "every transcript in the corpus". 64 files comfortably covers
// groved's live-session set (plus their sidechains) while keeping a corpus-wide
// usage/blocks pass from pinning a multi-GB corpus for the process lifetime;
// 200k entries caps the heap regardless of how large those 64 files are. A
// dropped entry only costs a re-parse.
const (
	usageCacheMaxFiles   = 64
	usageCacheMaxEntries = 200_000

	// usageHeadFingerprintBytes is how much of a file's head is hashed to
	// establish file identity across a size change.
	usageHeadFingerprintBytes = 4096
)

// usageFileCache makes live session summaries incremental. Claude transcripts
// are append-only JSONL, so a growing file only requires decoding bytes after
// the last complete newline. Truncation, and any replacement that changes the
// file head, falls back to a full parse.
type usageFileCacheEntry struct {
	// lastAccess and count are read by the evictor without holding mu.
	lastAccess atomic.Int64 // usageCacheTick value of the last lookup
	count      atomic.Int64 // len(entries) as last published

	mu       sync.Mutex
	size     int64
	modTime  int64
	complete bool
	// head is the SHA-256 of the first headLen bytes of the file as of the
	// snapshot. Resuming from a byte offset is only sound if the bytes before
	// that offset are still the same bytes we parsed; size alone does not
	// establish that (an in-place rsync/restore/editor rewrite can leave a
	// larger file with entirely different content, which would otherwise be
	// mistaken for an append and parsed from a mid-line offset).
	head    [sha256.Size]byte
	headLen int64
	entries []loadedEntry
}

var (
	usageFileCache sync.Map // path -> *usageFileCacheEntry

	usageCacheTick    atomic.Int64 // monotonic clock for LRU ordering
	usageCacheFiles   atomic.Int64 // number of paths in usageFileCache
	usageCacheEntries atomic.Int64 // approximate total cached entries
	usageCacheEvictMu sync.Mutex   // serializes eviction sweeps
)

func loadFileEntries(path, sessionID, projectPath string) ([]loadedEntry, error) {
	// Stat before touching the cache: a path we cannot stat must not pin an
	// entry (and a cache slot) for the lifetime of the process.
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	cached := usageCacheEntryFor(path)
	cached.lastAccess.Store(usageCacheTick.Add(1))
	cached.mu.Lock()
	defer cached.mu.Unlock()

	if cached.size == stat.Size() && cached.modTime == stat.ModTime().UnixNano() {
		return cloneLoadedEntries(cached.entries, sessionID, projectPath), nil
	}

	offset := int64(0)
	base := []loadedEntry(nil)
	if cached.complete && cached.size > 0 && stat.Size() > cached.size && cached.headUnchanged(path) {
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
	// parse again rather than publishing an ambiguous byte offset. The head
	// fingerprint is taken from the same generation as the parse — the re-stat
	// below validates both.
	head, headLen, headOK := fileHeadFingerprint(path, stat.Size())
	after, statErr := os.Stat(path)
	if headOK && statErr == nil && after.Size() == stat.Size() && after.ModTime().UnixNano() == stat.ModTime().UnixNano() {
		cached.size = stat.Size()
		cached.modTime = stat.ModTime().UnixNano()
		cached.complete = fileEndsWithNewline(path, stat.Size())
		cached.head, cached.headLen = head, headLen
		cached.entries = all
		usageCacheEntries.Add(int64(len(all)) - cached.count.Swap(int64(len(all))))
		// Safe to run while holding cached.mu: eviction never acquires an
		// entry's mutex, it only reads atomics and deletes map keys.
		maybeEvictUsageCache()
	}
	return cloneLoadedEntries(all, sessionID, projectPath), nil
}

// usageCacheEntryFor returns the cache entry for path, creating it if needed
// and keeping the file counter in step with map membership.
func usageCacheEntryFor(path string) *usageFileCacheEntry {
	if value, ok := usageFileCache.Load(path); ok {
		return value.(*usageFileCacheEntry)
	}
	value, loaded := usageFileCache.LoadOrStore(path, &usageFileCacheEntry{})
	if !loaded {
		usageCacheFiles.Add(1)
	}
	return value.(*usageFileCacheEntry)
}

// headUnchanged reports whether the file's head still hashes to the fingerprint
// recorded with the snapshot, i.e. whether the cached byte offset still refers
// to the same content. Callers must hold e.mu.
func (e *usageFileCacheEntry) headUnchanged(path string) bool {
	if e.headLen <= 0 {
		return false
	}
	sum, n, ok := fileHeadFingerprint(path, e.headLen)
	return ok && n == e.headLen && sum == e.head
}

// fileHeadFingerprint hashes the first min(size, usageHeadFingerprintBytes)
// bytes of path, returning the digest and how many bytes it covers.
func fileHeadFingerprint(path string, size int64) (sum [sha256.Size]byte, n int64, ok bool) {
	n = size
	if n > usageHeadFingerprintBytes {
		n = usageHeadFingerprintBytes
	}
	if n <= 0 {
		return sha256.Sum256(nil), 0, true
	}
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, false
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return sum, 0, false
	}
	return sha256.Sum256(buf), n, true
}

// maybeEvictUsageCache runs a sweep when the cheap counters say the cache may
// be over its bounds. The counters are approximations (a sweep may race a
// concurrent publish); every sweep recomputes them from the map, so any drift
// self-corrects.
func maybeEvictUsageCache() {
	if usageCacheFiles.Load() <= usageCacheMaxFiles && usageCacheEntries.Load() <= usageCacheMaxEntries {
		return
	}
	evictUsageCache()
}

// evictUsageCache drops least-recently-accessed paths until the cache is within
// both bounds. Dropping a path is always safe: a concurrent reader holding the
// evicted entry keeps using it under its own mutex, and the next lookup for
// that path simply re-parses into a fresh entry.
func evictUsageCache() {
	usageCacheEvictMu.Lock()
	defer usageCacheEvictMu.Unlock()

	type candidate struct {
		path  string
		last  int64
		count int64
	}
	var candidates []candidate
	var total int64
	usageFileCache.Range(func(key, value any) bool {
		entry := value.(*usageFileCacheEntry)
		count := entry.count.Load()
		total += count
		candidates = append(candidates, candidate{path: key.(string), last: entry.lastAccess.Load(), count: count})
		return true
	})

	files := int64(len(candidates))
	if files > usageCacheMaxFiles || total > usageCacheMaxEntries {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].last < candidates[j].last })
		for _, c := range candidates {
			if files <= usageCacheMaxFiles && total <= usageCacheMaxEntries {
				break
			}
			// Always keep the most recently used path, even when it alone
			// exceeds the entry cap; evicting it would just thrash.
			if files <= 1 {
				break
			}
			if _, ok := usageFileCache.LoadAndDelete(c.path); ok {
				files--
				total -= c.count
			}
		}
	}
	usageCacheFiles.Store(files)
	usageCacheEntries.Store(total)
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
	usageCacheEvictMu.Lock()
	defer usageCacheEvictMu.Unlock()
	usageFileCache.Range(func(key, _ any) bool {
		usageFileCache.Delete(key)
		return true
	})
	usageCacheFiles.Store(0)
	usageCacheEntries.Store(0)
}
