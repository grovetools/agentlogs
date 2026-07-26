package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func cachedUsageLine(id string, input int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":"test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", id, input)
}

func TestLoadFileEntriesIncrementalAppendAndTruncate(t *testing.T) {
	resetUsageFileCache()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(cachedUsageLine("one", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := loadFileEntries(path, "s", "p")
	if err != nil || len(first) != 1 {
		t.Fatalf("first load = %d, %v", len(first), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(cachedUsageLine("two", 2))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadFileEntries(path, "s", "p")
	if err != nil || len(second) != 2 || second[1].MessageID != "two" {
		t.Fatalf("append load = %#v, %v", second, err)
	}

	if err := os.WriteFile(path, []byte(cachedUsageLine("replacement", 3)), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced, err := loadFileEntries(path, "s2", "p2")
	if err != nil || len(replaced) != 1 || replaced[0].MessageID != "replacement" {
		t.Fatalf("replacement load = %#v, %v", replaced, err)
	}
	if replaced[0].SessionID != "s2" || replaced[0].ProjectPath != "p2" {
		t.Fatalf("cached tags not rebound: %#v", replaced[0])
	}
}

// A transcript replaced in place (rsync/restore of ~/.claude, editor rewrite)
// with different, larger content must not be mistaken for an append: resuming
// at the old size would drop the new head and splice stale entries in front of
// arbitrary mid-file ones.
func TestLoadFileEntriesDetectsInPlaceReplacement(t *testing.T) {
	resetUsageFileCache()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	original := cachedUsageLine("old-a", 1) + cachedUsageLine("old-b", 2)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := loadFileEntries(path, "s", "p")
	if err != nil || len(first) != 2 {
		t.Fatalf("first load = %d, %v", len(first), err)
	}

	// Strictly larger than the original, and different from byte 0 on.
	replacement := cachedUsageLine("replacement-a", 10) +
		cachedUsageLine("replacement-b", 20) +
		cachedUsageLine("replacement-c", 30)
	if len(replacement) <= len(original) {
		t.Fatalf("replacement (%d bytes) must be larger than original (%d bytes)", len(replacement), len(original))
	}
	if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadFileEntries(path, "s", "p")
	if err != nil {
		t.Fatalf("replacement load: %v", err)
	}
	var total int64
	for _, e := range got {
		if strings.HasPrefix(e.MessageID, "old-") {
			t.Fatalf("stale entry %q survived an in-place replacement: %#v", e.MessageID, got)
		}
		total += int64(e.Usage.InputTokens)
	}
	if len(got) != 3 || total != 60 {
		t.Fatalf("replacement load = %d entries, %d input tokens; want 3, 60 (%#v)", len(got), total, got)
	}

	// The replacement itself must still be cached as a coherent snapshot, and
	// appends onto it must resume correctly.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(cachedUsageLine("replacement-d", 40))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	appended, err := loadFileEntries(path, "s", "p")
	if err != nil || len(appended) != 4 || appended[3].MessageID != "replacement-d" {
		t.Fatalf("append after replacement = %#v, %v", appended, err)
	}
}

func usageCacheSize() int {
	n := 0
	usageFileCache.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// The cache must not grow without bound: a corpus-wide usage/blocks pass in a
// long-lived process (groved) would otherwise pin every transcript it touched.
func TestUsageFileCacheStaysBounded(t *testing.T) {
	resetUsageFileCache()
	dir := t.TempDir()
	paths := make([]string, 0, usageCacheMaxFiles*2)
	for i := 0; i < usageCacheMaxFiles*2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("session-%03d.jsonl", i))
		if err := os.WriteFile(path, []byte(cachedUsageLine(fmt.Sprintf("m-%d", i), i)), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		if _, err := loadFileEntries(path, "s", "p"); err != nil {
			t.Fatal(err)
		}
		if got := usageCacheSize(); got > usageCacheMaxFiles {
			t.Fatalf("cache holds %d files after %d loads; cap is %d", got, i+1, usageCacheMaxFiles)
		}
	}

	// Evicted paths must still load correctly — a drop only costs a re-parse.
	entries, err := loadFileEntries(paths[0], "s", "p")
	if err != nil || len(entries) != 1 || entries[0].MessageID != "m-0" {
		t.Fatalf("reload of evicted path = %#v, %v", entries, err)
	}
	if got := usageCacheSize(); got > usageCacheMaxFiles {
		t.Fatalf("cache holds %d files after reload; cap is %d", got, usageCacheMaxFiles)
	}
}

// A path that cannot be stat'd must not pin a cache slot forever.
func TestLoadFileEntriesDoesNotCacheStatFailures(t *testing.T) {
	resetUsageFileCache()
	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	for i := 0; i < 3; i++ {
		if _, err := loadFileEntries(missing, "s", "p"); err == nil {
			t.Fatal("expected an error for a missing transcript")
		}
	}
	if got := usageCacheSize(); got != 0 {
		t.Fatalf("stat failures pinned %d cache entries", got)
	}
}

func TestLoadFileEntriesConcurrent(t *testing.T) {
	resetUsageFileCache()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(cachedUsageLine("one", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries, err := loadFileEntries(path, "s", "p")
			if err != nil || len(entries) != 1 {
				t.Errorf("load = %d, %v", len(entries), err)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkLoadFileEntriesCached(b *testing.B) {
	resetUsageFileCache()
	path := filepath.Join(b.TempDir(), "session.jsonl")
	var data []byte
	for i := 0; i < 10000; i++ {
		data = append(data, []byte(cachedUsageLine(fmt.Sprintf("m-%d", i), i))...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	if _, err := loadFileEntries(path, "s", "p"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := loadFileEntries(path, "s", "p"); err != nil {
			b.Fatal(err)
		}
	}
}
