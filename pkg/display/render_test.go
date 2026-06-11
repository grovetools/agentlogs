package display

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

func sampleEntry() transcript.UnifiedEntry {
	return transcript.UnifiedEntry{
		Role:     "assistant",
		Provider: "claude",
		Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "Let me check the file."}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{
				ID:    "t1",
				Name:  "bash",
				Input: map[string]interface{}{"command": "ls -la"},
			}},
			{Type: "tool_result", Content: transcript.UnifiedToolResult{
				ToolCallID: "t1",
				Output:     "file1.go\nfile2.go",
			}},
			{Type: "reasoning", Content: transcript.UnifiedReasoning{Text: "thinking about files"}},
		},
	}
}

func renderMarkdown(t *testing.T, entry transcript.UnifiedEntry, detail string) string {
	t.Helper()
	var buf bytes.Buffer
	opts := RenderOptions{Style: StyleMarkdown, DetailLevel: detail}
	if err := RenderUnifiedEntry(&buf, entry, opts, DefaultToolFormatters()); err != nil {
		t.Fatalf("RenderUnifiedEntry failed: %v", err)
	}
	return buf.String()
}

// TestMarkdownDeterminism verifies markdown output is byte-identical across
// renders regardless of environment variables that influence terminal
// theming (GROVE_ICONS, CLICOLOR_FORCE), and contains no ANSI escapes.
func TestMarkdownDeterminism(t *testing.T) {
	entry := sampleEntry()

	t.Setenv("GROVE_ICONS", "nerd")
	t.Setenv("CLICOLOR_FORCE", "1")
	first := renderMarkdown(t, entry, "full")

	t.Setenv("GROVE_ICONS", "")
	t.Setenv("CLICOLOR_FORCE", "0")
	t.Setenv("NO_COLOR", "1")
	second := renderMarkdown(t, entry, "full")

	if first != second {
		t.Errorf("markdown output differs across environments:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Contains(first, "\x1b") {
		t.Errorf("markdown output contains ANSI escape sequences:\n%q", first)
	}
	if !strings.Contains(first, "**Assistant:**") {
		t.Errorf("markdown output missing stable role label:\n%s", first)
	}
	if !strings.Contains(first, "**Tool: Bash**") {
		t.Errorf("markdown output missing tool label:\n%s", first)
	}
	if !strings.Contains(first, "**Thinking:**") {
		t.Errorf("markdown output missing thinking label:\n%s", first)
	}
}

// TestMarkdownUserRoleLabel verifies user entries get a stable User label.
func TestMarkdownUserRoleLabel(t *testing.T) {
	entry := transcript.UnifiedEntry{
		Role: "user",
		Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "do the thing"}},
		},
	}
	out := renderMarkdown(t, entry, "full")
	if !strings.Contains(out, "**User:**") {
		t.Errorf("expected **User:** label, got:\n%s", out)
	}
}

// TestMarkdownInjectionSafety verifies tool output containing triple
// backticks cannot break out of the preformatted block: every content line
// is indented 4 spaces, so no line starts with ``` at column 0.
func TestMarkdownInjectionSafety(t *testing.T) {
	malicious := "```bash\nrm -rf /\n```\n# Fake Heading\n**bold injection**"
	entry := transcript.UnifiedEntry{
		Role: "assistant",
		Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{
				ToolCallID: "t1",
				Output:     malicious,
			}},
		},
	}

	out := renderMarkdown(t, entry, "full")

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "```") {
			t.Errorf("unindented fence escaped the block: %q", line)
		}
		if strings.HasPrefix(line, "# ") {
			t.Errorf("unindented heading escaped the block: %q", line)
		}
	}
	if !strings.Contains(out, "    ```bash") {
		t.Errorf("expected backtick content indented by 4 spaces, got:\n%s", out)
	}
	if !strings.Contains(out, "    # Fake Heading") {
		t.Errorf("expected heading content indented by 4 spaces, got:\n%s", out)
	}
}

// TestMarkdownCapBehavior verifies long tool outputs are capped at 500 lines
// with a "(N more lines)" note in full detail, and collapsed in summary.
func TestMarkdownCapBehavior(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	entry := transcript.UnifiedEntry{
		Role: "assistant",
		Parts: []transcript.UnifiedPart{
			{Type: "tool_result", Content: transcript.UnifiedToolResult{
				ToolCallID: "t1",
				Output:     sb.String(),
			}},
		},
	}

	full := renderMarkdown(t, entry, "full")
	if !strings.Contains(full, "    line 500") {
		t.Errorf("expected line 500 present in capped output")
	}
	if strings.Contains(full, "line 501") {
		t.Errorf("expected line 501 to be truncated")
	}
	if !strings.Contains(full, "... (100 more lines)") {
		t.Errorf("expected '(100 more lines)' truncation note, got:\n%s", full[len(full)-200:])
	}

	summary := renderMarkdown(t, entry, "summary")
	if !strings.Contains(summary, "(600 lines)") {
		t.Errorf("expected summary collapse '(600 lines)', got:\n%s", summary)
	}
	if strings.Contains(summary, "line 1\n") {
		t.Errorf("expected summary to omit raw content")
	}
}

// TestTerminalStyleRegression verifies the writer-based terminal renderer
// produces the same bytes as DisplayUnifiedEntry (stdout wrapper) and
// FormatUnifiedEntry.
func TestTerminalStyleRegression(t *testing.T) {
	entry := sampleEntry()
	toolFormatters := DefaultToolFormatters()

	var buf bytes.Buffer
	opts := RenderOptions{Style: StyleTerminal, DetailLevel: "full"}
	if err := RenderUnifiedEntry(&buf, entry, opts, toolFormatters); err != nil {
		t.Fatalf("RenderUnifiedEntry failed: %v", err)
	}

	// Capture stdout of DisplayUnifiedEntry.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	DisplayUnifiedEntry(entry, "full", toolFormatters)
	w.Close()
	os.Stdout = old
	captured, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(captured) != buf.String() {
		t.Errorf("DisplayUnifiedEntry output diverged from RenderUnifiedEntry:\ndisplay:\n%q\nrender:\n%q", captured, buf.String())
	}

	formatted := FormatUnifiedEntry(entry, "full", toolFormatters)
	if formatted != strings.TrimRight(buf.String(), "\n") {
		t.Errorf("FormatUnifiedEntry diverged from RenderUnifiedEntry:\nformat:\n%q\nrender:\n%q", formatted, buf.String())
	}
}

// TestDefaultStyleIsTerminal verifies an empty style falls back to terminal.
func TestDefaultStyleIsTerminal(t *testing.T) {
	entry := sampleEntry()
	toolFormatters := DefaultToolFormatters()

	var defaultBuf, terminalBuf bytes.Buffer
	if err := RenderUnifiedEntry(&defaultBuf, entry, RenderOptions{DetailLevel: "full"}, toolFormatters); err != nil {
		t.Fatalf("RenderUnifiedEntry failed: %v", err)
	}
	if err := RenderUnifiedEntry(&terminalBuf, entry, RenderOptions{Style: StyleTerminal, DetailLevel: "full"}, toolFormatters); err != nil {
		t.Fatalf("RenderUnifiedEntry failed: %v", err)
	}
	if defaultBuf.String() != terminalBuf.String() {
		t.Errorf("default style output differs from explicit terminal style")
	}
}

// TestParseRenderStyle verifies flag validation.
func TestParseRenderStyle(t *testing.T) {
	if s, err := ParseRenderStyle(""); err != nil || s != StyleTerminal {
		t.Errorf("empty style: got (%v, %v), want (terminal, nil)", s, err)
	}
	if s, err := ParseRenderStyle("terminal"); err != nil || s != StyleTerminal {
		t.Errorf("terminal style: got (%v, %v), want (terminal, nil)", s, err)
	}
	if s, err := ParseRenderStyle("markdown"); err != nil || s != StyleMarkdown {
		t.Errorf("markdown style: got (%v, %v), want (markdown, nil)", s, err)
	}
	if _, err := ParseRenderStyle("html"); err == nil {
		t.Errorf("expected error for unknown style")
	}
}
