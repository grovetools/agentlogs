package display

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/theme"

	"github.com/grovetools/agentlogs/pkg/formatters"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// RenderStyle selects the output style for transcript rendering.
type RenderStyle string

const (
	// StyleTerminal renders with lipgloss colors and theme icons for
	// interactive terminal display.
	StyleTerminal RenderStyle = "terminal"
	// StyleMarkdown renders environment-independent markdown suitable for
	// durable files: stable role labels, 4-space-indented tool blocks, no
	// theme/TTY/color dependence.
	StyleMarkdown RenderStyle = "markdown"
)

// markdownOutputCapLines is the maximum number of lines emitted for a single
// tool input/output block in markdown mode before truncation.
const markdownOutputCapLines = 500

// RenderOptions controls transcript rendering.
type RenderOptions struct {
	// Style selects terminal or markdown output. Defaults to StyleTerminal
	// when empty.
	Style RenderStyle
	// DetailLevel is "summary" or "full".
	DetailLevel string
}

// ParseRenderStyle validates a style string (e.g. from a CLI flag).
func ParseRenderStyle(s string) (RenderStyle, error) {
	switch RenderStyle(s) {
	case "", StyleTerminal:
		return StyleTerminal, nil
	case StyleMarkdown:
		return StyleMarkdown, nil
	default:
		return "", fmt.Errorf("unknown render style %q (expected 'terminal' or 'markdown')", s)
	}
}

// RenderUnifiedEntry renders a single UnifiedEntry to w in the requested
// style. The toolFormatters registry is only consulted in terminal style;
// markdown style renders tool input/output itself using an injection-safe
// 4-space-indent rule.
func RenderUnifiedEntry(
	w io.Writer,
	entry transcript.UnifiedEntry,
	opts RenderOptions,
	toolFormatters map[string]formatters.ToolFormatter,
) error {
	switch opts.Style {
	case StyleMarkdown:
		return renderMarkdownEntry(w, entry, opts)
	default:
		return renderTerminalEntry(w, entry, opts.DetailLevel, toolFormatters)
	}
}

// RenderUnifiedTranscript renders a full transcript to w.
func RenderUnifiedTranscript(
	w io.Writer,
	entries []transcript.UnifiedEntry,
	opts RenderOptions,
	toolFormatters map[string]formatters.ToolFormatter,
) error {
	for _, entry := range entries {
		if err := RenderUnifiedEntry(w, entry, opts, toolFormatters); err != nil {
			return err
		}
	}
	return nil
}

// --- Terminal style ---

// renderTerminalEntry renders an entry with lipgloss colors and theme icons.
// This is the original DisplayUnifiedEntry logic, parameterized over a writer.
func renderTerminalEntry(
	w io.Writer,
	entry transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) error {
	robotToolStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Green)
	robotTextStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.LightText)
	userStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Yellow)
	mutedStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)

	robotToolIcon := robotToolStyle.Render(theme.IconRobot) // Green for tool calls
	robotTextIcon := robotTextStyle.Render(theme.IconRobot) // White for text responses
	userIcon := userStyle.Render(theme.IconChevron)
	tree := mutedStyle.Render(treeChar)

	// For user messages, display text content and tool results
	if entry.Role == "user" {
		var textParts []string
		var hasToolResults bool

		for _, part := range entry.Parts {
			switch part.Type {
			case "text":
				if content, ok := part.Content.(transcript.UnifiedTextContent); ok && content.Text != "" {
					textParts = append(textParts, content.Text)
				} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
					if text, ok := contentMap["text"].(string); ok && text != "" {
						textParts = append(textParts, text)
					}
				}
			case "tool_result":
				// Show tool results with tree connector (these belong to previous tool call)
				output := partToolResultOutput(part)
				if output != "" {
					hasToolResults = true
					// For long outputs (like file reads), show a summary
					lines := strings.Split(strings.TrimSpace(output), "\n")
					if len(lines) > 5 {
						// Show compact summary
						fmt.Fprintf(w, "  %s  %s\n", tree, mutedStyle.Render(fmt.Sprintf("(%d lines)", len(lines))))
					} else {
						// Show short output directly
						for i, line := range lines {
							if strings.TrimSpace(line) != "" {
								if i == 0 {
									fmt.Fprintf(w, "  %s  %s\n", tree, line)
								} else {
									fmt.Fprintf(w, "     %s\n", line)
								}
							}
						}
					}
				}
			}
		}

		if hasToolResults {
			fmt.Fprintln(w) // Blank line after tool results
		}

		if len(textParts) > 0 {
			fmt.Fprintf(w, "%s %s\n\n", userIcon, strings.Join(textParts, "\n"))
		}
		return nil
	}

	// For assistant messages, render parts in order to preserve interleaving
	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			text := partText(part)
			if text != "" {
				fmt.Fprintf(w, "%s %s\n\n", robotTextIcon, text)
			}

		case "tool_call":
			toolCall := partToolCall(part)

			toolDisplay := formatUnifiedToolCall(toolCall, detailLevel, toolFormatters, mutedStyle)
			if toolDisplay != "" {
				fmt.Fprintf(w, "%s %s\n", robotToolIcon, toolDisplay)
			}

			// Show output with tree connector (for embedded output like OpenCode or merged Claude)
			if toolCall.Output != "" {
				outputDisplay := formatToolOutput(toolCall.Name, toolCall.Output, mutedStyle)
				if outputDisplay != "" {
					fmt.Fprintf(w, "  %s  %s\n", tree, mutedStyle.Render(outputDisplay))
				}
				// Add blank line after embedded output (OpenCode or merged Claude results)
				fmt.Fprintln(w)
			}

		case "reasoning":
			text := partReasoningText(part)
			if text != "" {
				// Format thinking with "∴ Thinking…" header in italic
				italicMuted := mutedStyle.Italic(true)
				fmt.Fprintln(w, italicMuted.Render("∴ Thinking…"))
				fmt.Fprintln(w) // Blank line after header
				for _, line := range strings.Split(text, "\n") {
					if strings.TrimSpace(line) != "" {
						fmt.Fprintln(w, italicMuted.Render("  "+line))
					} else {
						fmt.Fprintln(w) // Preserve paragraph breaks
					}
				}
				fmt.Fprintln(w) // Blank line after thinking
			}

		case "tool_result":
			// Tool results shown with tree connector (only first line gets ⎿)
			output := partToolResultOutput(part)
			if output != "" {
				lines := strings.Split(strings.TrimSpace(output), "\n")
				if len(lines) > 5 {
					// Compact summary for long output
					fmt.Fprintf(w, "  %s  %s\n", tree, mutedStyle.Render(fmt.Sprintf("(%d lines)", len(lines))))
				} else {
					firstLine := true
					for _, line := range lines {
						if strings.TrimSpace(line) != "" {
							if firstLine {
								fmt.Fprintf(w, "  %s  %s\n", tree, line)
								firstLine = false
							} else {
								fmt.Fprintf(w, "     %s\n", line)
							}
						}
					}
				}
			}
			fmt.Fprintln(w) // Blank line after tool result (even if empty)
		}
	}
	return nil
}

// --- Markdown style ---

// renderMarkdownEntry renders an entry as environment-independent markdown:
// stable role labels, tool input/output as 4-space-indented preformatted
// blocks (injection-safe against content containing markdown fences), no
// theme/TTY/lipgloss dependence.
func renderMarkdownEntry(w io.Writer, entry transcript.UnifiedEntry, opts RenderOptions) error {
	roleLabel := "**Assistant:**"
	if entry.Role == "user" {
		roleLabel = "**User:**"
	}

	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			text := partText(part)
			if text != "" {
				fmt.Fprintf(w, "%s\n\n%s\n\n", roleLabel, text)
			}

		case "reasoning":
			text := partReasoningText(part)
			if text != "" {
				fmt.Fprintf(w, "**Thinking:**\n\n")
				writeIndentedBlock(w, text, opts.DetailLevel)
				fmt.Fprintln(w)
			}

		case "tool_call":
			toolCall := partToolCall(part)
			name := capitalizeFirst(toolCall.Name)
			if name == "" {
				name = "(unknown)"
			}
			fmt.Fprintf(w, "**Tool: %s**\n\n", name)
			if len(toolCall.Input) > 0 {
				if inputJSON, err := json.MarshalIndent(toolCall.Input, "", "  "); err == nil {
					writeIndentedBlock(w, string(inputJSON), opts.DetailLevel)
					fmt.Fprintln(w)
				}
			}
			if toolCall.Output != "" {
				fmt.Fprintf(w, "**Tool Output:**\n\n")
				writeIndentedBlock(w, toolCall.Output, opts.DetailLevel)
				fmt.Fprintln(w)
			}

		case "tool_result":
			output := partToolResultOutput(part)
			if output != "" {
				fmt.Fprintf(w, "**Tool Result:**\n\n")
				writeIndentedBlock(w, output, opts.DetailLevel)
				fmt.Fprintln(w)
			}
		}
	}
	return nil
}

// writeIndentedBlock writes text as a 4-space-indented preformatted markdown
// block. Indenting (instead of fencing) is injection-safe: content containing
// triple backticks cannot break out of the block. Output is capped at
// markdownOutputCapLines lines with a "(N more lines)" note; in summary
// detail, blocks longer than 5 lines collapse to a "(N lines)" note.
func writeIndentedBlock(w io.Writer, text string, detailLevel string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	if detailLevel == "summary" && len(lines) > 5 {
		fmt.Fprintf(w, "    (%d lines)\n", len(lines))
		return
	}

	capped := lines
	remaining := 0
	if len(lines) > markdownOutputCapLines {
		capped = lines[:markdownOutputCapLines]
		remaining = len(lines) - markdownOutputCapLines
	}

	for _, line := range capped {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(w)
		} else {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
	if remaining > 0 {
		fmt.Fprintf(w, "    ... (%d more lines)\n", remaining)
	}
}

// --- Part content extraction helpers ---

// partText extracts text from a "text" part, handling both typed and
// map-decoded content.
func partText(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedTextContent); ok {
		return content.Text
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		text, _ := contentMap["text"].(string)
		return text
	}
	return ""
}

// partReasoningText extracts text from a "reasoning" part.
func partReasoningText(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedReasoning); ok {
		return content.Text
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		return getStringField(contentMap, "text")
	}
	return ""
}

// partToolResultOutput extracts output from a "tool_result" part.
func partToolResultOutput(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedToolResult); ok {
		return content.Output
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		return getStringField(contentMap, "output")
	}
	return ""
}

// partToolCall extracts a UnifiedToolCall from a "tool_call" part.
func partToolCall(part transcript.UnifiedPart) transcript.UnifiedToolCall {
	if content, ok := part.Content.(transcript.UnifiedToolCall); ok {
		return content
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		toolCall := transcript.UnifiedToolCall{
			ID:     getStringField(contentMap, "id"),
			Name:   getStringField(contentMap, "name"),
			Status: getStringField(contentMap, "status"),
			Output: getStringField(contentMap, "output"),
			Title:  getStringField(contentMap, "title"),
			Diff:   getStringField(contentMap, "diff"),
		}
		if input, ok := contentMap["input"].(map[string]interface{}); ok {
			toolCall.Input = input
		}
		return toolCall
	}
	return transcript.UnifiedToolCall{}
}
