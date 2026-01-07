package display

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// DisplayUnifiedEntry renders a single UnifiedEntry with consistent formatting.
func DisplayUnifiedEntry(
	entry transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	robotStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Violet)
	userStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Yellow)
	mutedStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)

	// For user messages, display all text content together
	if entry.Role == "user" {
		var textParts []string
		for _, part := range entry.Parts {
			if part.Type == "text" {
				if content, ok := part.Content.(transcript.UnifiedTextContent); ok && content.Text != "" {
					textParts = append(textParts, content.Text)
				} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
					if text, ok := contentMap["text"].(string); ok && text != "" {
						textParts = append(textParts, text)
					}
				}
			}
		}
		if len(textParts) > 0 {
			roleIcon := userStyle.Render(theme.IconLightbulb)
			fmt.Printf("%s %s\n\n", roleIcon, strings.Join(textParts, "\n"))
		}
		return
	}

	// For assistant messages, render parts in order to preserve interleaving
	role := robotStyle.Render(theme.IconRobot)

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
				fmt.Printf("%s %s\n\n", role, text)
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
				fmt.Printf("%s %s\n", role, toolDisplay)
			}

		case "reasoning":
			var text string
			if content, ok := part.Content.(transcript.UnifiedReasoning); ok {
				text = content.Text
			} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
				text = getStringField(contentMap, "text")
			}
			if text != "" {
				// Format thinking with "∴ Thinking…" header
				fmt.Print(mutedStyle.Render("∴ Thinking…"))
				fmt.Println()
				for _, line := range strings.Split(text, "\n") {
					fmt.Println(mutedStyle.Render("  " + line))
				}
			}

		case "tool_result":
			// Tool results shown in full mode
			if detailLevel == "full" {
				var output string
				if content, ok := part.Content.(transcript.UnifiedToolResult); ok {
					output = content.Output
				} else if contentMap, ok := part.Content.(map[string]interface{}); ok {
					output = getStringField(contentMap, "output")
				}
				if output != "" && len(output) < 500 {
					fmt.Printf("%s %s\n", role, mutedStyle.Render(fmt.Sprintf("  Output: %s", output)))
				}
			}
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

// formatUnifiedToolCall formats a tool call for display.
func formatUnifiedToolCall(
	tool transcript.UnifiedToolCall,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
	mutedStyle lipgloss.Style,
) string {
	// Check for specialized formatter first
	if toolFormatters != nil {
		if formatter, ok := toolFormatters[tool.Name]; ok {
			inputJSON, _ := json.Marshal(tool.Input)
			if formatted := formatter(inputJSON, detailLevel); formatted != "" {
				return formatted
			}
		}
	}

	// Full detail mode
	if detailLevel == "full" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("▼ %s", tool.Name))
		if tool.Title != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", tool.Title))
		}
		sb.WriteString("\n")

		if len(tool.Input) > 0 {
			prettyInput, err := json.MarshalIndent(tool.Input, "  ", "  ")
			if err == nil {
				sb.WriteString(mutedStyle.Render(fmt.Sprintf("  Input: %s\n", string(prettyInput))))
			}
		}

		if tool.Diff != "" {
			diff := tool.Diff
			lines := strings.Split(diff, "\n")
			if len(lines) > 20 {
				diff = strings.Join(lines[:20], "\n") + "\n... (truncated)"
			}
			sb.WriteString(mutedStyle.Render(fmt.Sprintf("  Diff:\n%s\n", diff)))
		} else if tool.Output != "" && len(tool.Output) < 500 {
			sb.WriteString(mutedStyle.Render(fmt.Sprintf("  Output: %s\n", tool.Output)))
		}

		return sb.String()
	}

	// Summary mode
	toolInfo := fmt.Sprintf("[Using %s", tool.Name)

	// Extract common parameters for summary
	if filePath, ok := tool.Input["file_path"].(string); ok {
		toolInfo += fmt.Sprintf(" on %s", filePath)
	} else if filePath, ok := tool.Input["filePath"].(string); ok {
		toolInfo += fmt.Sprintf(" on %s", filePath)
	} else if command, ok := tool.Input["command"].(string); ok {
		if len(command) > 50 {
			toolInfo += fmt.Sprintf(": %s...", command[:50])
		} else {
			toolInfo += fmt.Sprintf(": %s", command)
		}
	} else if pattern, ok := tool.Input["pattern"].(string); ok {
		toolInfo += fmt.Sprintf(" for '%s'", pattern)
	} else if tool.Title != "" {
		toolInfo += fmt.Sprintf(" (%s)", tool.Title)
	}

	toolInfo += "]"
	return toolInfo
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
