package usage

import (
	"fmt"
	"os"
	"path/filepath"
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
