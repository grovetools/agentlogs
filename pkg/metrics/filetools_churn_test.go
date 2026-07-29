package metrics

import (
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

func touch(t *testing.T, provider, tool string, input map[string]interface{}) FileTouch {
	t.Helper()
	got, ok := ToolCallFileTouch(provider, transcript.UnifiedToolCall{ID: tool, Name: tool, Input: input})
	if !ok {
		t.Fatalf("ToolCallFileTouch(%s, %s) = not resolved, want a touch", provider, tool)
	}
	return got
}

func TestEditChurnTrimsUntouchedAnchorLines(t *testing.T) {
	// The agent anchors its edit on two surrounding lines and rewrites one in
	// the middle: git would call that +1 -1, and so must we — counting the
	// anchors would report +3 -3.
	got := touch(t, "claude", "Edit", map[string]interface{}{
		"file_path":  "/repo/pkg/a.go",
		"old_string": "func a() {\n\treturn 1\n}",
		"new_string": "func a() {\n\treturn 2\n}",
	})
	if got.LinesAdded != 1 || got.LinesRemoved != 1 {
		t.Errorf("churn = +%d -%d, want +1 -1", got.LinesAdded, got.LinesRemoved)
	}
	if !got.Edit || got.Path != "/repo/pkg/a.go" {
		t.Errorf("touch = %+v, want an edit of /repo/pkg/a.go", got)
	}
}

func TestEditChurnCountsPureInsertionAndDeletion(t *testing.T) {
	insert := touch(t, "claude", "Edit", map[string]interface{}{
		"file_path":  "/repo/a.go",
		"old_string": "keep\n",
		"new_string": "keep\nadded one\nadded two\n",
	})
	if insert.LinesAdded != 2 || insert.LinesRemoved != 0 {
		t.Errorf("insertion churn = +%d -%d, want +2 -0", insert.LinesAdded, insert.LinesRemoved)
	}

	del := touch(t, "claude", "Edit", map[string]interface{}{
		"file_path":  "/repo/a.go",
		"old_string": "keep\ngone one\ngone two\n",
		"new_string": "keep\n",
	})
	if del.LinesAdded != 0 || del.LinesRemoved != 2 {
		t.Errorf("deletion churn = +%d -%d, want +0 -2", del.LinesAdded, del.LinesRemoved)
	}
}

func TestMultiEditChurnSumsEveryReplacement(t *testing.T) {
	got := touch(t, "claude", "MultiEdit", map[string]interface{}{
		"file_path": "/repo/a.go",
		"edits": []interface{}{
			map[string]interface{}{"old_string": "one", "new_string": "uno"},
			map[string]interface{}{"old_string": "two\nthree", "new_string": "dos"},
		},
	})
	if got.LinesAdded != 2 || got.LinesRemoved != 3 {
		t.Errorf("churn = +%d -%d, want +2 -3", got.LinesAdded, got.LinesRemoved)
	}
}

func TestUnmeasurableMutationsReportZeroChurn(t *testing.T) {
	// Write carries only the new contents: a 3-line write could be a new file or
	// a rewrite of a 300-line one, so the churn stays unmeasured rather than
	// asserting a number that would contradict git.
	write := touch(t, "claude", "Write", map[string]interface{}{
		"file_path": "/repo/a.go",
		"content":   "one\ntwo\nthree\n",
	})
	if !write.Edit {
		t.Error("Write should register as a mutation")
	}
	if write.LinesAdded != 0 || write.LinesRemoved != 0 {
		t.Errorf("write churn = +%d -%d, want unmeasured 0/0", write.LinesAdded, write.LinesRemoved)
	}

	// A read is a read: no churn even though the path resolves.
	read := touch(t, "claude", "Read", map[string]interface{}{"file_path": "/repo/a.go"})
	if read.Edit || read.LinesAdded != 0 || read.LinesRemoved != 0 {
		t.Errorf("read touch = %+v, want a non-mutating 0/0 touch", read)
	}

	// pi's edit is a mutation whose argument spelling is unattested, so it too
	// reports unmeasured churn rather than a guess.
	pi := touch(t, "pi", "edit", map[string]interface{}{"path": "/repo/a.go"})
	if !pi.Edit || pi.LinesAdded != 0 || pi.LinesRemoved != 0 {
		t.Errorf("pi edit touch = %+v, want a mutation with unmeasured churn", pi)
	}
}

func TestIdenticalReplacementIsNoChurn(t *testing.T) {
	got := touch(t, "claude", "Edit", map[string]interface{}{
		"file_path":  "/repo/a.go",
		"old_string": "same",
		"new_string": "same",
	})
	if got.LinesAdded != 0 || got.LinesRemoved != 0 {
		t.Errorf("churn = +%d -%d, want 0/0", got.LinesAdded, got.LinesRemoved)
	}
}
