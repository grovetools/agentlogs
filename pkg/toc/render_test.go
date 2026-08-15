package toc

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

func renderFixtureItems() []Item {
	return BuildAgentItems([]transcript.UnifiedEntry{
		{Role: "user", MessageID: "u1", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "Please fix the flaky retry test"}},
		}},
		{Role: "assistant", MessageID: "a1", Parts: []transcript.UnifiedPart{
			{Type: "text", Content: transcript.UnifiedTextContent{Text: "The retry is missing.\n\n# Root cause\nThe loop never re-arms.\n\n## Fix\nRe-arm it."}},
			{Type: "tool_call", Content: transcript.UnifiedToolCall{ID: "t1", Name: "Bash", Input: map[string]interface{}{"command": "go test ./..."}}},
			{Type: "tool_result", Content: transcript.UnifiedToolResult{ToolCallID: "t1", Output: "ok"}},
		}},
	}, BuildOptions{Provider: "claude"})
}

// RenderStyled must be deterministic: even with no terminal on stdout (this
// test process), styled output carries real ANSI, and the plain render is
// exactly the styled render with escapes stripped — same layout, same cells.
func TestRenderStyledIsStyledOffTerminalAndPlainMatchesStripped(t *testing.T) {
	items := renderFixtureItems()
	opts := DefaultRenderOptions(60, "claude")
	styled := RenderStyled(items, opts)
	plain := RenderPlain(items, opts)
	if !strings.Contains(styled, "\x1b[") {
		t.Fatal("RenderStyled produced no ANSI styling off-terminal")
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatal("RenderPlain leaked ANSI escapes")
	}
	if plain != ansi.Strip(styled) {
		t.Fatal("plain render diverged from stripped styled render")
	}
}

func TestRenderShowsRolesBoxesHeadingsAndTools(t *testing.T) {
	plain := RenderPlain(renderFixtureItems(), DefaultRenderOptions(60, "claude"))
	for _, want := range []string{
		"You",                             // prompt role label
		"Claude",                          // assistant role label from provider
		"╭",                               // transcript box top corner
		"╰",                               // transcript box bottom corner
		"Please fix the flaky retry test", // prompt body inside its bubble
		"Root cause",                      // heading row inside the response box
		"go test ./...",                   // tool row summary
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered outline missing %q:\n%s", want, plain)
		}
	}
	// No cursor, no selection, no footer in the non-interactive render.
	if strings.Contains(plain, "follow:") || strings.Contains(plain, "markdown:") {
		t.Errorf("non-interactive render leaked interactive footer:\n%s", plain)
	}
	// The page must fit its width.
	for _, line := range strings.Split(plain, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Errorf("line exceeds width (%d cells): %q", w, line)
		}
	}
}

func TestRenderWithoutMarkdownKeepsOneLineSummaries(t *testing.T) {
	opts := DefaultRenderOptions(60, "claude")
	opts.ShowMarkdown = false
	plain := RenderPlain(renderFixtureItems(), opts)
	if strings.Contains(plain, "╭") {
		t.Errorf("outline mode rendered transcript boxes:\n%s", plain)
	}
	if !strings.Contains(plain, "The retry is missing.") {
		t.Errorf("outline mode lost the assistant summary line:\n%s", plain)
	}
	if strings.Contains(plain, "Claude") {
		t.Errorf("outline mode replaced the summary with the role label:\n%s", plain)
	}
}
