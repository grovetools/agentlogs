package agentstream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeWorkflowFixture builds a minimal session dir with one wf_ run:
// a journal with two started events and one result (one agent in-flight),
// plus one agent transcript with a single user line.
func writeWorkflowFixture(t *testing.T) string {
	t.Helper()
	sessionDir := t.TempDir()
	runDir := filepath.Join(sessionDir, "subagents", "workflows", "wf_test01")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	journal := `{"type":"started","key":"v2:k1","agentId":"aaa111"}
{"type":"started","key":"v2:k2","agentId":"bbb222"}
{"type":"result","key":"v2:k1","agentId":"aaa111","result":{"summary":"ok"}}
`
	if err := os.WriteFile(filepath.Join(runDir, "journal.jsonl"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}

	agentLine := `{"type":"user","isSidechain":true,"agentId":"aaa111","message":{"role":"user","content":"do the thing"}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "agent-aaa111.jsonl"), []byte(agentLine), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionDir
}

func collectEntries(t *testing.T, sessionDir string, d time.Duration) map[string]map[string]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	ch, err := StreamWorkflow(ctx, sessionDir)
	if err != nil {
		t.Fatalf("StreamWorkflow: %v", err)
	}

	// byProvider[provider][agentID] = count
	byProvider := make(map[string]map[string]int)
	for entry := range ch {
		if byProvider[entry.Provider] == nil {
			byProvider[entry.Provider] = make(map[string]int)
		}
		byProvider[entry.Provider][entry.AgentID]++
	}
	return byProvider
}

func TestStreamWorkflowFansInJournalAndAgents(t *testing.T) {
	sessionDir := writeWorkflowFixture(t)
	byProvider := collectEntries(t, sessionDir, 2*time.Second)

	journal := byProvider["journal"]
	if len(journal) == 0 {
		t.Fatal("no journal entries streamed")
	}
	// Journal events carry their own agent IDs (started+result for aaa111, started for bbb222).
	if journal["aaa111"] != 2 {
		t.Errorf("journal entries for aaa111 = %d, want 2", journal["aaa111"])
	}
	if journal["bbb222"] != 1 {
		t.Errorf("journal entries for bbb222 = %d, want 1 (in-flight agent still has its started event)", journal["bbb222"])
	}

	claude := byProvider["claude"]
	if claude["aaa111"] != 1 {
		t.Errorf("claude transcript entries for aaa111 = %d, want 1", claude["aaa111"])
	}
}

func TestStreamWorkflowDiscoversLateFiles(t *testing.T) {
	sessionDir := writeWorkflowFixture(t)
	runDir := filepath.Join(sessionDir, "subagents", "workflows", "wf_test01")

	go func() {
		// Land a new agent transcript after streaming has started; the
		// 500ms discovery poll must pick it up.
		time.Sleep(700 * time.Millisecond)
		line := `{"type":"user","isSidechain":true,"agentId":"ccc333","message":{"role":"user","content":"late arrival"}}` + "\n"
		_ = os.WriteFile(filepath.Join(runDir, "agent-ccc333.jsonl"), []byte(line), 0o644)
	}()

	byProvider := collectEntries(t, sessionDir, 3*time.Second)
	if byProvider["claude"]["ccc333"] != 1 {
		t.Errorf("late-added agent transcript not discovered: %v", byProvider["claude"])
	}
}

func TestStreamWorkflowMissingSessionDir(t *testing.T) {
	if _, err := StreamWorkflow(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing session dir")
	}
}

func TestStreamWorkflowNoWorkflowsIsQuietNoOp(t *testing.T) {
	// Session dir exists but has no subagents/workflows — stream must stay
	// open and empty, not error (the workflow may simply not have started).
	sessionDir := t.TempDir()
	byProvider := collectEntries(t, sessionDir, 800*time.Millisecond)
	if len(byProvider) != 0 {
		t.Errorf("expected no entries, got %v", byProvider)
	}
}
