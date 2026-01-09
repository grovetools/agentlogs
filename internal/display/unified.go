package display

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
)

var ulog = grovelogging.NewUnifiedLogger("grove-agent-logs.display")

// Formatting constants for output
const (
	treeChar = "⎿" // Tree connector for sub-content
)

// DisplayUnifiedEntry renders a single UnifiedEntry with consistent formatting.
func DisplayUnifiedEntry(
	entry transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	robotToolStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Green)
	robotTextStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.LightText)
	userStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Yellow)
	mutedStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)

	robotToolIcon := robotToolStyle.Render(theme.IconRobot)  // Green for tool calls
	robotTextIcon := robotTextStyle.Render(theme.IconRobot)  // White for text responses
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
				var output string
				if content, ok := part.Content.(transcript.UnifiedToolResult); ok {
					output = content.Output
				} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
					output = getStringField(contentMap, "output")
				}
				if output != "" {
					ctx := context.Background()
					hasToolResults = true
					// For long outputs (like file reads), show a summary
					lines := strings.Split(strings.TrimSpace(output), "\n")
					if len(lines) > 5 {
						// Show compact summary
						ulog.Info("Tool result").
							Field("line_count", len(lines)).
							Pretty(fmt.Sprintf("  %s  %s\n", tree, mutedStyle.Render(fmt.Sprintf("(%d lines)", len(lines))))).
							PrettyOnly().
							Log(ctx)
					} else {
						// Show short output directly
						for i, line := range lines {
							if strings.TrimSpace(line) != "" {
								if i == 0 {
									ulog.Info("Tool result").
										Pretty(fmt.Sprintf("  %s  %s\n", tree, line)).
										PrettyOnly().
										Log(ctx)
								} else {
									ulog.Info("Tool result continuation").
										Pretty(fmt.Sprintf("     %s\n", line)).
										PrettyOnly().
										Log(ctx)
								}
							}
						}
					}
				}
			}
		}

		if hasToolResults {
			ctx := context.Background()
			ulog.Info("Tool results separator").
				Pretty("\n").
				PrettyOnly().
				Log(ctx)
		}

		if len(textParts) > 0 {
			ctx := context.Background()
			ulog.Info("User message").
				Field("role", "user").
				Pretty(fmt.Sprintf("%s %s\n\n", userIcon, strings.Join(textParts, "\n"))).
				PrettyOnly().
				Log(ctx)
		}
		return
	}

	// For assistant messages, render parts in order to preserve interleaving
	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			var text string
			if content, ok := part.Content.(transcript.UnifiedTextContent); ok {
				text = content.Text
			} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
				text, _ = contentMap["text"].(string)
			}
			if text != "" {
				ctx := context.Background()
				ulog.Info("Assistant text").
					Field("role", "assistant").
					Pretty(fmt.Sprintf("%s %s\n\n", robotTextIcon, text)).
					PrettyOnly().
					Log(ctx)
			}

		case "tool_call":
			var toolCall transcript.UnifiedToolCall
			if content, ok := part.Content.(transcript.UnifiedToolCall); ok {
				toolCall = content
			} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
				toolCall = transcript.UnifiedToolCall{
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
			}

			toolDisplay := formatUnifiedToolCall(toolCall, detailLevel, toolFormatters, mutedStyle)
			if toolDisplay != "" {
				ctx := context.Background()
				ulog.Info("Tool call").
					Field("tool_name", toolCall.Name).
					Field("tool_id", toolCall.ID).
					Pretty(fmt.Sprintf("%s %s\n", robotToolIcon, toolDisplay)).
					PrettyOnly().
					Log(ctx)
			}

			// Show output with tree connector (for embedded output like OpenCode or merged Claude)
			if toolCall.Output != "" {
				ctx := context.Background()
				outputDisplay := formatToolOutput(toolCall.Name, toolCall.Output, mutedStyle)
				if outputDisplay != "" {
					ulog.Info("Tool output").
						Field("tool_name", toolCall.Name).
						Pretty(fmt.Sprintf("  %s  %s\n", tree, mutedStyle.Render(outputDisplay))).
						PrettyOnly().
						Log(ctx)
				}
				// Add blank line after embedded output (OpenCode or merged Claude results)
				ulog.Info("Tool output separator").
					Pretty("\n").
					PrettyOnly().
					Log(ctx)
			}

		case "reasoning":
			var text string
			if content, ok := part.Content.(transcript.UnifiedReasoning); ok {
				text = content.Text
			} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
				text = getStringField(contentMap, "text")
			}
			if text != "" {
				ctx := context.Background()
				// Format thinking with "∴ Thinking…" header in italic
				italicMuted := mutedStyle.Italic(true)
				ulog.Info("Reasoning header").
					Pretty(italicMuted.Render("∴ Thinking…") + "\n").
					PrettyOnly().
					Log(ctx)
				ulog.Info("Reasoning spacer").
					Pretty("\n").
					PrettyOnly().
					Log(ctx)
				for _, line := range strings.Split(text, "\n") {
					if strings.TrimSpace(line) != "" {
						ulog.Info("Reasoning line").
							Pretty(italicMuted.Render("  "+line) + "\n").
							PrettyOnly().
							Log(ctx)
					} else {
						ulog.Info("Reasoning paragraph break").
							Pretty("\n").
							PrettyOnly().
							Log(ctx)
					}
				}
				ulog.Info("Reasoning end spacer").
					Pretty("\n").
					PrettyOnly().
					Log(ctx)
			}

		case "tool_result":
			// Tool results shown with tree connector (only first line gets ⎿)
			var output string
			if content, ok := part.Content.(transcript.UnifiedToolResult); ok {
				output = content.Output
			} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
				output = getStringField(contentMap, "output")
			}
			if output != "" {
				ctx := context.Background()
				lines := strings.Split(strings.TrimSpace(output), "\n")
				if len(lines) > 5 {
					// Compact summary for long output
					ulog.Info("Tool result summary").
						Field("line_count", len(lines)).
						Pretty(fmt.Sprintf("  %s  %s\n", tree, mutedStyle.Render(fmt.Sprintf("(%d lines)", len(lines))))).
						PrettyOnly().
						Log(ctx)
				} else {
					firstLine := true
					for _, line := range lines {
						if strings.TrimSpace(line) != "" {
							if firstLine {
								ulog.Info("Tool result").
									Pretty(fmt.Sprintf("  %s  %s\n", tree, line)).
									PrettyOnly().
									Log(ctx)
								firstLine = false
							} else {
								ulog.Info("Tool result continuation").
									Pretty(fmt.Sprintf("     %s\n", line)).
									PrettyOnly().
									Log(ctx)
							}
						}
					}
				}
			}
			ctx := context.Background()
			ulog.Info("Tool result separator").
				Pretty("\n").
				PrettyOnly().
				Log(ctx)
		}
	}
}

// getStringField safely extracts a string field from a map.
func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// formatToolOutput formats tool output, with special handling for read-like tools.
// Returns a simple string without leading/trailing whitespace - caller handles indentation.
func formatToolOutput(toolName string, output string, mutedStyle lipgloss.Style) string {
	if output == "" {
		return ""
	}

	// For read tools, show a summary instead of full content
	toolLower := strings.ToLower(toolName)
	if toolLower == "read" || strings.Contains(toolLower, "read") {
		lines := strings.Split(output, "\n")
		lineCount := len(lines)
		// Trim trailing empty lines from count
		for lineCount > 0 && strings.TrimSpace(lines[lineCount-1]) == "" {
			lineCount--
		}
		if lineCount > 5 {
			return fmt.Sprintf("(%d lines read)", lineCount)
		}
	}

	// For short outputs, show the content
	output = strings.TrimSpace(output)
	if len(output) < 200 {
		return fmt.Sprintf("Output: %s", output)
	}

	// For longer outputs, truncate
	lines := strings.Split(output, "\n")
	if len(lines) > 5 {
		return fmt.Sprintf("Output: (%d lines)", len(lines))
	}

	return fmt.Sprintf("Output: %s", output)
}

// formatUnifiedToolCall formats a tool call for display.
// Uses consistent ToolName(arg) format for all tools.
func formatUnifiedToolCall(
	tool transcript.UnifiedToolCall,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
	mutedStyle lipgloss.Style,
) string {
	// Capitalize tool name for consistency
	toolName := capitalizeFirst(tool.Name)

	// Format as ToolName(key_arg) for consistency
	keyArg := extractKeyArg(tool)

	var display string
	if keyArg != "" {
		display = fmt.Sprintf("%s(%s)", toolName, keyArg)
	} else if tool.Title != "" {
		display = fmt.Sprintf("%s(%s)", toolName, tool.Title)
	} else {
		display = toolName
	}

	return display
}

// capitalizeFirst capitalizes the first letter of a string.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// extractKeyArg extracts the most relevant argument for inline display.
func extractKeyArg(tool transcript.UnifiedToolCall) string {
	// Check common parameter names in order of preference
	if cmd, ok := tool.Input["command"].(string); ok {
		// For commands, show a truncated version
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 60 {
			return cmd[:57] + "..."
		}
		return cmd
	}

	if filePath, ok := tool.Input["file_path"].(string); ok {
		return shortenPath(filePath)
	}

	if filePath, ok := tool.Input["filePath"].(string); ok {
		return shortenPath(filePath)
	}

	if pattern, ok := tool.Input["pattern"].(string); ok {
		return pattern
	}

	if query, ok := tool.Input["query"].(string); ok {
		if len(query) > 40 {
			return query[:37] + "..."
		}
		return query
	}

	if url, ok := tool.Input["url"].(string); ok {
		return url
	}

	return ""
}

// shortenPath shortens a file path for display, keeping the filename and some context.
func shortenPath(path string) string {
	if len(path) <= 50 {
		return path
	}

	// Try to show last 2-3 path components
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}

	// Show .../<parent>/<file>
	shortened := ".../" + strings.Join(parts[len(parts)-2:], "/")
	if len(shortened) > 50 {
		// Just show the filename
		return ".../" + parts[len(parts)-1]
	}
	return shortened
}

// DisplayUnifiedTranscript displays a full transcript.
func DisplayUnifiedTranscript(
	entries []transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	for _, entry := range entries {
		DisplayUnifiedEntry(entry, detailLevel, toolFormatters)
	}
}
