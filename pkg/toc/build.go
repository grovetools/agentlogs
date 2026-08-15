package toc

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/grovetools/agentlogs/pkg/display"
	"github.com/grovetools/agentlogs/pkg/transcript"
	"github.com/grovetools/core/tui/components/markdown"
	"github.com/grovetools/core/tui/theme"
)

// BuildOptions parameterizes BuildAgentItems with the session context that
// used to live on treemux's tracker.
type BuildOptions struct {
	// Provider is the agent provider behind the transcript ("claude", "pi",
	// …). Pi rows with duplicated visible text keep their stable entry/tool ID
	// anchors and merely drop the text fallback; other providers drop the
	// action entirely.
	Provider string
	// Markers binds the $WT/$JA/$NB path-marker vocabulary to the session's
	// actual directories. The zero value substitutes nothing.
	Markers PathMarkers
	// SourceGeneration stamps every built item, so an interactive consumer can
	// reject jumps against a stale source. Zero is fine for one-shot renders.
	SourceGeneration uint64
}

// BuildAgentItems folds a provider-normalized transcript into a flat outline
// with fold-tree metadata (Level/HasKids/DescendantCount) and per-row jump
// snippets. Expanded carries the DEFAULT fold state for each row; interactive
// consumers overlay their own user fold state on top.
func BuildAgentItems(entries []transcript.UnifiedEntry, opts BuildOptions) []Item {
	items := make([]Item, 0, len(entries)*2)

	// Pi and some other providers normalize a tool result as a later assistant
	// entry. Associate it by stable call ID, never merely by adjacency, and hide
	// a consumed result-only transport entry from the conversational outline.
	resultsByCall := make(map[string]transcript.UnifiedToolResult)
	pendingCalls := make(map[string]struct{})
	consumedResultEntry := make(map[int]bool)
	for entryIndex, entry := range entries {
		resultOnly := len(entry.Parts) > 0
		matched := false
		for _, part := range entry.Parts {
			switch content := part.Content.(type) {
			case transcript.UnifiedToolCall:
				if content.ID != "" {
					pendingCalls[content.ID] = struct{}{}
				}
				resultOnly = false
			case transcript.UnifiedToolResult:
				if _, ok := pendingCalls[content.ToolCallID]; ok {
					resultsByCall[content.ToolCallID] = content
					delete(pendingCalls, content.ToolCallID)
					matched = true
				}
			default:
				resultOnly = false
			}
		}
		if resultOnly && matched {
			consumedResultEntry[entryIndex] = true
		}
	}

	for entryIndex, entry := range entries {
		if consumedResultEntry[entryIndex] {
			continue
		}
		entryID := entry.MessageID
		if entryID == "" {
			entryID = fmt.Sprintf("entry-%d", entryIndex)
		}
		var texts []string
		var tools []transcript.UnifiedToolCall
		results := make(map[string]transcript.UnifiedToolResult)
		var reasoning []string
		for _, part := range entry.Parts {
			switch content := part.Content.(type) {
			case transcript.UnifiedTextContent:
				if part.Type == "text" && strings.TrimSpace(content.Text) != "" {
					texts = append(texts, content.Text)
				}
			case transcript.UnifiedToolCall:
				tools = append(tools, content)
			case transcript.UnifiedToolResult:
				results[content.ToolCallID] = content
			case transcript.UnifiedReasoning:
				if strings.TrimSpace(content.Text) != "" {
					reasoning = append(reasoning, content.Text)
				}
			}
		}
		text := strings.TrimSpace(strings.Join(texts, "\n"))
		if entry.Role == "user" {
			title := firstNonemptyLine(text)
			if title == "" {
				title = "User prompt"
			}
			// Extension-injected turns (subjob reports landing, for one) reach
			// the model as user messages but nobody typed them. Reading them as
			// prompts made a fleet of returning children look like a fleet of
			// human interruptions.
			kind := "prompt"
			if entry.CustomType != "" {
				kind = "notice"
			}
			snippet := SearchSnippet(title)
			items = append(items, Item{ID: entryID, EntryID: entryID, Kind: kind, Title: title, Snippet: snippet, Level: 1, Markdown: text, SourceGeneration: opts.SourceGeneration, Actionable: snippet != ""})
			continue
		}

		// Each prose response is a fold root. Markdown headings remain distinct
		// outline rows beneath it, preserving their H1-H6 hierarchy while tools
		// and reasoning stay peer activity rows after the response.
		headings := markdown.ExtractHeadings(strings.Split(text, "\n"))
		rootTitle, rootSnippet, rootStatus, hasRoot := agentResponseSummary(text, headings)
		if hasRoot {
			items = append(items, Item{ID: entryID + ":response", EntryID: entryID, Kind: "assistant", Title: rootTitle, Snippet: rootSnippet, Status: rootStatus, Level: 1, Markdown: text, SourceGeneration: opts.SourceGeneration, Actionable: rootSnippet != ""})
		}
		for _, h := range headings {
			snippet := SearchSnippet(h.Text)
			level := 1
			if hasRoot {
				level = h.Level + 1
			}
			items = append(items, Item{ID: fmt.Sprintf("%s:h:%d", entryID, h.Line), EntryID: entryID, Kind: "heading", Title: h.Text, Snippet: snippet, Level: level, HeadingLevel: h.Level, MarkdownLine: h.Line, SourceGeneration: opts.SourceGeneration, Actionable: snippet != ""})
		}

		for i, tool := range tools {
			summary := stripLeadingToolIcon(display.ToolCallSummary(tool))
			result, hasResult := results[tool.ID]
			if associated, ok := resultsByCall[tool.ID]; ok {
				result, hasResult = associated, true
			}
			// The jump target must still be text the pane actually printed, which
			// is the untouched summary; only the outline's title is compacted.
			snippet := SearchSnippet(summary)
			title := summary
			if compacted, ok := compactToolCall(tool, opts.Markers); ok {
				title = stripLeadingToolIcon(display.ToolCallSummary(compacted))
			}
			title = agentToolOutlineTitle(tool, title)
			anchorID := tool.ID
			if anchorID == "" {
				anchorID = entryID
			}
			items = append(items, Item{ID: fmt.Sprintf("%s:tool:%d", entryID, i), EntryID: anchorID, Kind: "tool", Tool: tool.Name, Title: title, Status: toolResultStatus(tool, result, hasResult), Snippet: snippet, Level: 1, SourceGeneration: opts.SourceGeneration, Actionable: snippet != ""})
		}
		for i, thought := range reasoning {
			lines := len(strings.Split(strings.TrimSpace(thought), "\n"))
			summary := fmt.Sprintf("thinking (%d lines)", lines)
			// Pi renders only this summary for collapsed reasoning; the reasoning
			// body itself is not terminal text. Searching the hidden first thought
			// line made every such row advertise a jump that could never succeed —
			// so the jump target stays the visible summary while the title shows
			// the thought itself, the only thing that tells consecutive
			// "thinking (1 lines)" rows apart.
			title := compactTitlePaths(firstNonemptyLine(thought), opts.Markers)
			if title == "" {
				title = summary
			}
			snippet := SearchSnippet(summary)
			items = append(items, Item{ID: fmt.Sprintf("%s:reasoning:%d", entryID, i), EntryID: entryID, Kind: "reasoning", Title: title, Snippet: snippet, Level: 1, SourceGeneration: opts.SourceGeneration, Actionable: snippet != ""})
		}
	}
	// Repeated text cannot identify one scrollback location truthfully. Pi rows
	// remain actionable through stable entry/tool IDs, but their text fallback
	// is cleared so a missing anchor cannot silently choose the wrong duplicate.
	snippetCounts := make(map[string]int)
	for _, item := range items {
		if item.Snippet != "" {
			snippetCounts[item.Snippet]++
		}
	}
	for i := range items {
		if items[i].Actionable && snippetCounts[items[i].Snippet] > 1 {
			if opts.Provider == "pi" && items[i].EntryID != "" {
				items[i].Snippet = ""
			} else {
				items[i].Actionable = false
			}
		}
		items[i].HasKids = i+1 < len(items) && items[i+1].Level > items[i].Level
		for j := i + 1; j < len(items) && items[j].Level > items[i].Level; j++ {
			items[i].DescendantCount++
		}
		items[i].Expanded = items[i].Kind != "tools" && items[i].Kind != "reasoning"
	}
	return items
}

// stripLeadingToolIcon drops the glyph the shared summarizer's specialized
// formatters prefix to Read/Write/Edit/TodoWrite summaries. That prefix earns
// its keep in a transcript whose every row starts with the same robot, but the
// outline renders an icon chosen from the tool itself — leaving it would
// print two glyphs, and for Edit two glyphs that disagree. Matching the exact
// icon strings (rather than "any symbol") keeps this honest in both the Nerd
// Font and ASCII sets, and leaves real titles untouched.
func stripLeadingToolIcon(summary string) string {
	for _, icon := range []string{theme.IconFile, theme.IconFilePlus, theme.IconChecklist} {
		if icon == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(summary, icon+" "); ok {
			return strings.TrimLeft(rest, " ")
		}
	}
	return summary
}

// tocPathTailSegments is how much of a path survives compaction: the penultimate
// directory plus the leaf. Every tool row in one worktree shares a long absolute
// prefix, so width-clipped outline titles all rendered as the same "Editing
// /Users/solair/.loca" regardless of which file was touched. Dropping the shared
// prefix moves the part that actually differs into the visible cells.
const tocPathTailSegments = 2

// agentToolOutlineTitle enriches outline-only titles with arguments that the
// shared transcript summary intentionally does not use. Keep the original
// summary as the jump snippet: Claude currently prints TaskCreate/TaskUpdate
// without these details, while the outline still needs to distinguish them.
func agentToolOutlineTitle(tool transcript.UnifiedToolCall, fallback string) string {
	name := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(tool.Name, "_", ""), "-", ""))
	cleanString := func(key string) string {
		value, _ := tool.Input[key].(string)
		return strings.Join(strings.Fields(value), " ")
	}
	switch name {
	case "taskcreate":
		if subject := cleanString("subject"); subject != "" {
			return fmt.Sprintf("TaskCreate(%s)", subject)
		}
	case "taskupdate":
		taskID := cleanString("taskId")
		if taskID == "" {
			taskID = cleanString("task_id")
		}
		status := strings.ReplaceAll(cleanString("status"), "_", " ")
		activeForm := cleanString("activeForm")
		if activeForm == "" {
			activeForm = cleanString("active_form")
		}
		label := ""
		if taskID != "" {
			label = "#" + taskID
		}
		if activeForm != "" {
			if label != "" {
				label += ": "
			}
			label += activeForm
		}
		if status != "" {
			if label != "" {
				label += " → "
			}
			label += status
		}
		if label != "" {
			return fmt.Sprintf("TaskUpdate(%s)", label)
		}
	}
	return fallback
}

// absolutePathInTitle matches an absolute or home-relative path that starts at a
// token boundary. Go's regexp has no lookbehind, so the boundary is captured and
// re-emitted; requiring it keeps relative fragments whose slash is mid-token
// (./..., a/b/c, http://…) out of the match.
var absolutePathInTitle = regexp.MustCompile(`(^|[\s"'(\[=:,])(~?/[^\s"'()\[\]]*)`)

// compactToolCall returns a copy of a tool call whose string inputs have had
// their paths compacted, and reports whether anything changed. The outline
// summarizes that copy rather than compacting the finished summary because the
// shared summarizer caps a command at 60 characters: "cd <worktree path> && go
// test ./..." spends every one of them on the prefix all rows have in common,
// and the part that identifies the row is truncated away before the outline can
// see it. The whole transcript is re-summarized on every streamed entry, so a
// call with no compactable path reports false and is summarized only once.
func compactToolCall(tool transcript.UnifiedToolCall, markers PathMarkers) (transcript.UnifiedToolCall, bool) {
	changed := false
	compact := func(text string) string {
		compacted := compactTitlePaths(text, markers)
		if compacted != text {
			changed = true
		}
		return compacted
	}
	tool.Title = compact(tool.Title)
	input := make(map[string]interface{}, len(tool.Input))
	for key, value := range tool.Input {
		switch typed := value.(type) {
		case string:
			input[key] = compact(typed)
		case []interface{}: // Codex-style argv commands.
			argv := make([]interface{}, len(typed))
			for i, arg := range typed {
				if text, ok := arg.(string); ok {
					argv[i] = compact(text)
					continue
				}
				argv[i] = arg
			}
			input[key] = argv
		default:
			input[key] = value
		}
	}
	if !changed {
		return tool, false
	}
	tool.Input = input
	return tool, true
}

// compactTitlePaths shortens every path in a row's text. This is an
// outline-only presentation change: snippets, jump anchors and the transcript
// pane itself keep the full path.
func compactTitlePaths(text string, markers PathMarkers) string {
	return absolutePathInTitle.ReplaceAllStringFunc(text, func(match string) string {
		groups := absolutePathInTitle.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		return groups[1] + compactPath(groups[2], markers)
	})
}

// compactPath renders a path the way a narrow outline can actually show it: the
// session's own directories collapse to their markers ($WT/$JA/$NB), and
// whatever remains deeper than the tail is elided down to the penultimate
// directory and the leaf.
func compactPath(path string, markers PathMarkers) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" || trimmed == "~" {
		return path
	}
	suffix := path[len(trimmed):]
	marker, body := "", trimmed
	if token, rest, exact, ok := markers.Match(trimmed); ok {
		if exact {
			return token + suffix
		}
		marker, body = token+"/", rest
	}
	if marker == "" {
		body = strings.TrimPrefix(strings.TrimPrefix(body, "~"), "/")
	}
	segments := strings.Split(body, "/")
	switch {
	case len(segments) > tocPathTailSegments+1:
		body = ".../" + strings.Join(segments[len(segments)-tocPathTailSegments:], "/")
	case marker == "":
		// Nothing was stripped and nothing is worth eliding: a one- or
		// two-segment prefix costs as many cells as the components it replaces.
		return path
	}
	return marker + body + suffix
}

func toolResultStatus(tool transcript.UnifiedToolCall, result transcript.UnifiedToolResult, hasResult bool) string {
	if hasResult {
		if result.IsError {
			return "failed"
		}
		return "completed"
	}
	switch strings.ToLower(tool.Status) {
	case "completed", "complete", "success", "succeeded", "done":
		return "completed"
	case "failed", "error":
		return "failed"
	case "pending", "running", "in_progress":
		return "running"
	}
	if tool.Output != "" {
		return "completed"
	}
	return "running"
}

func agentResponseSummary(text string, headings []markdown.Heading) (string, string, string, bool) {
	// Prefer actual prose: it is both more useful than a generic label and more
	// likely to be a distinctive line visible in terminal scrollback.
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !isConversationalSummary(line) {
			continue
		}
		return SearchSnippet(line), SearchSnippet(line), "", true
	}
	if len(headings) > 0 {
		s := SearchSnippet(headings[0].Text)
		return s, s, "", true
	}
	return "", "", "", false
}

func isConversationalSummary(line string) bool {
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
		return false
	}
	lower := strings.ToLower(line)
	for _, prefix := range []string{"package ", "import ", "func ", "return ", "var ", "const ", "type "} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func firstNonemptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return strings.TrimLeft(line, "# ")
		}
	}
	return ""
}

// AgentJumpSnippetRunes keeps a search target inside the narrowest practical
// agent pane row. Ghostty exposes soft-wrapped terminal rows separately, so a
// longer query that crosses a wrap is not present in FormatScrollbackPlain even
// though the user can read it continuously on screen.
const AgentJumpSnippetRunes = 24

// SearchSnippet normalizes a row's visible text into a short scrollback search
// target: whitespace collapsed, clipped to AgentJumpSnippetRunes at a word
// boundary where possible.
func SearchSnippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > AgentJumpSnippetRunes {
		clipped := string(runes[:AgentJumpSnippetRunes])
		// Prefer a whole visible word. A partial path/argument is more likely to
		// differ from Pi's compact rendering and is no more distinctive.
		if cut := strings.LastIndex(clipped, " "); cut > 0 {
			clipped = clipped[:cut]
		}
		text = clipped
	}
	return text
}
