package toc

import (
	"strings"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
	"github.com/grovetools/core/tui/theme"
)

func TestBuildAgentItemsGroupsResponsesAndHeadings(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "u1", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "Please fix the flaky retry test"}},
		}},
		{Role: "assistant", MessageID: "a1", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "The retry is missing.\n\n# Root cause\nprose\n\n## Fix\nmore prose"}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "t1", Name: "Bash", Input: map[string]interface{}{"command": "go test ./..."}}},
			{Type: "reasoning", Content: transcript.UnifiedReasoning{Text: "thinking about retries"}},
		}},
	}, BuildOptions{Provider: "claude", SourceGeneration: 7})

	var kinds []string
	for _, item := range items {
		kinds = append(kinds, item.Kind)
		if item.SourceGeneration != 7 {
			t.Fatalf("item %q generation = %d, want 7", item.ID, item.SourceGeneration)
		}
	}
	got := strings.Join(kinds, ",")
	for _, want := range []string{"prompt", "assistant", "heading", "tool", "reasoning"} {
		if !strings.Contains(got, want) {
			t.Fatalf("outline kinds %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "tools") {
		t.Fatalf("flat agent outline retained tool grouping row: %q", got)
	}
	if items[0].Snippet != "Please fix the flaky" {
		t.Fatalf("prompt snippet = %q", items[0].Snippet)
	}
	if items[1].Title != "The retry is missing." || items[1].Snippet != "The retry is missing." {
		t.Fatalf("assistant summary = title %q snippet %q", items[1].Title, items[1].Snippet)
	}
	if items[1].Markdown == "" || !items[1].HasKids {
		t.Fatalf("assistant response is not an inline-markdown fold root: %+v", items[1])
	}
	var rootHeading, fixHeading Item
	for _, item := range items {
		if item.Title == "Root cause" {
			rootHeading = item
		}
		if item.Title == "Fix" {
			fixHeading = item
		}
	}
	if rootHeading.Level != 2 || rootHeading.HeadingLevel != 1 || !rootHeading.HasKids {
		t.Fatalf("H1 hierarchy = %+v", rootHeading)
	}
	if fixHeading.Level != 3 || fixHeading.HeadingLevel != 2 {
		t.Fatalf("H2 hierarchy = %+v", fixHeading)
	}
	// Expanded carries the DEFAULT fold state: everything opens except
	// reasoning rows (and legacy grouped-tools rows).
	for _, item := range items {
		wantExpanded := item.Kind != "reasoning"
		if item.Expanded != wantExpanded {
			t.Fatalf("default Expanded for %q = %v: %+v", item.Kind, item.Expanded, item)
		}
	}
}

func TestTaskOutlineRowsIncludeCreateAndUpdateDetails(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{{
		Role: "assistant", MessageID: "tasks", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "one", Name: "TaskCreate", Input: map[string]interface{}{"subject": "Seed the lab", "description": "details"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "two", Name: "task_create", Input: map[string]interface{}{"subject": "Run the verifier"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "three", Name: "TaskUpdate", Input: map[string]interface{}{"taskId": "2", "status": "in_progress", "activeForm": "Running the verifier"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "four", Name: "task_update", Input: map[string]interface{}{"task_id": "2", "status": "completed"}}},
		},
	}}, BuildOptions{})

	var titles, snippets []string
	for _, item := range items {
		if item.Kind == "tool" {
			titles = append(titles, item.Title)
			snippets = append(snippets, item.Snippet)
		}
	}
	if got := strings.Join(titles, " | "); got != "TaskCreate(Seed the lab) | TaskCreate(Run the verifier) | TaskUpdate(#2: Running the verifier → in progress) | TaskUpdate(#2 → completed)" {
		t.Fatalf("task tool titles = %q", got)
	}
	if got := strings.Join(snippets, " | "); got != "TaskCreate | Task_create | TaskUpdate | Task_update" {
		t.Fatalf("task jump snippets changed from visible transcript text: %q", got)
	}
}

func TestAgentJumpTargetsMatchVisiblePiRowsAndDoNotCrossSoftWraps(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "prompt", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "Read the briefing file at '/Users/solair/a/path/that/wraps/in/the/agent/pane'"}}}},
		{Role: "assistant", MessageID: "work", Parts: []transcript.UnifiedPart{
			{Type: "reasoning", Content: transcript.UnifiedReasoning{Text: "This private reasoning body is not rendered by Pi"}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "call", Name: "Bash", Input: map[string]interface{}{"command": "rg -n hierarchy treemux/internal/toc and-a-long-tail"}}},
		}},
	}, BuildOptions{})

	for _, item := range items {
		if item.Actionable && len([]rune(item.Snippet)) > AgentJumpSnippetRunes {
			t.Fatalf("%s jump target crosses a likely soft wrap: %q", item.Kind, item.Snippet)
		}
		switch item.Kind {
		case "prompt":
			if item.Snippet != "Read the briefing file" {
				t.Fatalf("prompt snippet = %q", item.Snippet)
			}
		case "reasoning":
			if item.Snippet != "thinking (1 lines)" || !item.Actionable {
				t.Fatalf("reasoning row targets hidden body instead of visible summary: %+v", item)
			}
			if item.Title != "This private reasoning body is not rendered by Pi" {
				t.Fatalf("reasoning row title = %q, want the thought itself", item.Title)
			}
		}
	}
}

// TestTocTitlesCompactWorktreePathsBeforeSummarizing: within one worktree every
// tool row starts with the same long absolute prefix, so a narrow outline
// clipped them all to an identical "Bash(cd /Users/solair/.local/share/...)".
// Compaction has to happen on the tool input, not on the finished summary: the
// shared summarizer caps a command at 60 characters, and the shared prefix eats
// every one of them before the part that identifies the call is reached.
func TestTocTitlesCompactWorktreePathsBeforeSummarizing(t *testing.T) {
	const root = "/Users/solair/.local/share/grove/worktrees/grovetools-0bd46c64/misc-fixes"
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "work", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "a", Name: "Edit", Input: map[string]interface{}{
				"file_path": root + "/treemux/internal/toc/tracker.go", "old_string": "before", "new_string": "after",
			}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "b", Name: "Bash", Input: map[string]interface{}{"command": "cd " + root + "/treemux && go test ./internal/toc/"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "c", Name: "Bash", Input: map[string]interface{}{"command": "cd " + root + " && grove build"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "d", Name: "Read", Input: map[string]interface{}{"file_path": "/tmp/short.go"}}},
		}},
	}, BuildOptions{Markers: PathMarkers{Worktree: root}})

	var tools []Item
	for _, item := range items {
		if item.Kind == "tool" {
			tools = append(tools, item)
		}
	}
	if len(tools) != 4 {
		t.Fatalf("tool rows = %d: %+v", len(tools), items)
	}
	want := []string{
		"Editing " + WorktreeMarker + "/.../toc/tracker.go", // The Edit summary trails its diff.
		"Bash(cd " + WorktreeMarker + "/treemux && go test ./internal/toc/)",
		"Bash(cd " + WorktreeMarker + " && grove build)",
		"Reading /tmp/short.go", // Nothing to strip and nothing worth eliding.
	}
	for i, w := range want {
		if !strings.HasPrefix(tools[i].Title, w) {
			t.Errorf("title[%d] = %q, want prefix %q", i, tools[i].Title, w)
		}
	}
	for _, item := range items {
		if strings.Contains(item.Snippet, WorktreeMarker) {
			t.Errorf("jump target no longer matches the line the pane printed: %q", item.Snippet)
		}
	}
}

// TestTocPathsStayWholeWithoutAKnownWorktree: with no markers there is no
// prefix to strip, and only genuinely deep paths are elided.
func TestTocPathsStayWholeWithoutAKnownWorktree(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "work", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "a", Name: "Read", Input: map[string]interface{}{"file_path": "/etc/hosts"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "b", Name: "Read", Input: map[string]interface{}{"file_path": "/a/deep/nested/tree/file.go"}}},
		}},
	}, BuildOptions{})

	var titles []string
	for _, item := range items {
		if item.Kind == "tool" {
			titles = append(titles, item.Title)
		}
	}
	if len(titles) != 2 || titles[0] != "Reading /etc/hosts" || titles[1] != "Reading .../tree/file.go" {
		t.Fatalf("titles = %q", titles)
	}
}

func TestDuplicateVisibleAgentJumpTargetsAreNotAdvertised(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "one", Parts: []transcript.UnifiedPart{{Type: "reasoning", Content: transcript.UnifiedReasoning{Text: "hidden one"}}}},
		{Role: "assistant", MessageID: "two", Parts: []transcript.UnifiedPart{{Type: "reasoning", Content: transcript.UnifiedReasoning{Text: "hidden two"}}}},
	}, BuildOptions{})
	for _, item := range items {
		if item.Kind == "reasoning" && item.Actionable {
			t.Fatalf("ambiguous repeated visible summary advertised as jumpable: %+v", item)
		}
	}
}

func TestPiDuplicateAndWrappedLabelsUseStableEntryAndToolAnchors(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "prompt-one", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "same label that wraps across a narrow terminal width"}}}},
		{Role: "user", MessageID: "prompt-two", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "same label that wraps across a narrow terminal width"}}}},
		{Role: "assistant", MessageID: "assistant", Parts: []transcript.UnifiedPart{{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "tool-stable", Name: "Read", Input: map[string]interface{}{"path": "a/very/long/wrapped/path"}}}}},
	}, BuildOptions{Provider: "pi"})
	for _, item := range items {
		switch item.ID {
		case "prompt-one", "prompt-two":
			if !item.Actionable || item.Snippet != "" || item.EntryID != item.ID {
				t.Fatalf("duplicate Pi prompt did not retain semantic action: %+v", item)
			}
		}
		if item.Kind == "tool" && (!item.Actionable || item.EntryID != "tool-stable") {
			t.Fatalf("Pi tool anchor = %+v", item)
		}
	}
}

func TestAgentToolResultStatuses(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{{
		Role: "assistant", MessageID: "a-status", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "ok", Name: "Read", Input: map[string]interface{}{"file_path": "ok.go"}}},
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "ok", Output: "done"}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "bad", Name: "Bash", Input: map[string]interface{}{"command": "false"}}},
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "bad", Output: "exit 1", IsError: true}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "live", Name: "Bash", Input: map[string]interface{}{"command": "sleep 1"}}},
		},
	}}, BuildOptions{})
	statuses := map[string]string{}
	for _, item := range items {
		if item.Kind == "tool" {
			statuses[item.Status] = item.Title
		}
	}
	for _, want := range []string{"completed", "failed", "running"} {
		if statuses[want] == "" {
			t.Fatalf("tool statuses = %#v, missing %q", statuses, want)
		}
	}
}

func TestAgentResultOnlyAndEmptyEntriesAreOmitted(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "result-ok", Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "orphan-ok", Output: "wrote 3 files\nmore output"}},
		}},
		{Role: "assistant", MessageID: "result-failed", Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "orphan-bad", Output: "permission denied", IsError: true}},
		}},
		{Role: "assistant", MessageID: "empty"},
		{Role: "assistant", MessageID: "empty-result", Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "orphan-empty", IsError: true}},
		}},
	}, BuildOptions{})
	if len(items) != 0 {
		t.Fatalf("non-conversational result-only roots remained: %+v", items)
	}
}

func TestAgentCodeFragmentsDoNotBecomeResponseRoots(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "brace", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "}"}}}},
		{Role: "assistant", MessageID: "code", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "package panels\nreturn tuimux.CursorInfo{"}}}},
		{Role: "assistant", MessageID: "fence", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "```go\nfunc nope() {}\n```"}}}},
	}, BuildOptions{})
	if len(items) != 0 {
		t.Fatalf("code/output fragments became assistant roots: %+v", items)
	}
}

func TestAgentResultAssociatesToPriorToolByStableID(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "u", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "run checks"}}}},
		{Role: "assistant", MessageID: "call", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "stable-1", Name: "Bash", Input: map[string]interface{}{"command": "go test ./..."}}},
		}},
		{Role: "assistant", MessageID: "unrelated", Parts: []transcript.UnifiedPart{{Type: "text", Content: transcript.UnifiedTextContent{Text: "still working"}}}},
		{Role: "assistant", MessageID: "result", Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "stable-1", Output: "ok"}},
		}},
	}, BuildOptions{})
	for _, item := range items {
		if item.Kind == "tool" {
			if item.Status != "completed" {
				t.Fatalf("associated tool status = %q, want completed", item.Status)
			}
			return
		}
	}
	t.Fatal("associated tool row missing")
}

// TestExtensionInjectedTurnsAreNoticesNotPrompts: a subjob monitor reporting
// children home reaches the model as a user turn, so without the customType
// marker a fleet of returning children reads as a fleet of human
// interruptions. The row must be a notice; a typed prompt must stay a prompt.
func TestExtensionInjectedTurnsAreNoticesNotPrompts(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "typed", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "please land the branch"}},
		}},
		{Role: "user", MessageID: "injected", CustomType: "flow-subjob-events", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "Reports ready; join before using:\n- job-7"}},
		}},
	}, BuildOptions{})

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(items), items)
	}
	if items[0].Kind != "prompt" {
		t.Errorf("typed prompt kind = %q, want prompt", items[0].Kind)
	}
	if items[1].Kind != "notice" {
		t.Errorf("injected turn kind = %q, want notice", items[1].Kind)
	}
	if items[1].Title != "Reports ready; join before using:" {
		t.Errorf("notice title = %q", items[1].Title)
	}
}

// TestToolRowsCarryToolNameAndDropTheSummarizerGlyph: renderers pick a row's
// icon from the tool name, so the name must survive summarization — and the
// summarizer's own leading glyph must not, or Read/Edit/Write rows print two.
func TestToolRowsCarryToolNameAndDropTheSummarizerGlyph(t *testing.T) {
	items := BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "assistant", MessageID: "work", Parts: []transcript.UnifiedPart{
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "a", Name: "Read", Input: map[string]interface{}{"file_path": "/tmp/x.go"}}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "b", Name: "grove_concepts", Input: map[string]interface{}{"query": "nb-lifecycle"}}},
		}},
	}, BuildOptions{})

	var tools []Item
	for _, item := range items {
		if item.Kind == "tool" {
			tools = append(tools, item)
		}
	}
	if len(tools) != 2 {
		t.Fatalf("tool rows = %d, want 2: %+v", len(tools), items)
	}
	if tools[0].Tool != "Read" || tools[1].Tool != "grove_concepts" {
		t.Errorf("tool names = %q, %q", tools[0].Tool, tools[1].Tool)
	}
	if strings.HasPrefix(tools[0].Title, theme.IconFile) {
		t.Errorf("Read row title still carries the summarizer glyph: %q", tools[0].Title)
	}
	if !strings.HasPrefix(tools[0].Title, "Reading ") {
		t.Errorf("Read row title = %q, want it to start with the verb", tools[0].Title)
	}
}
