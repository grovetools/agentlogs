package display

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-agent-logs/internal/opencode"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
)

var ulogOpenCode = grovelogging.NewUnifiedLogger("grove-agent-logs.display.opencode")

// DisplayOpenCodeEntry formats and displays an OpenCode transcript entry.
func DisplayOpenCodeEntry(entry opencode.TranscriptEntry, detailLevel string) {
	mutedStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)
	robotStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Violet)
	userStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Yellow)

	var textParts []string
	var toolUses []string

	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			if textPart, ok := part.Content.(opencode.TextPart); ok && textPart.Text != "" {
				textParts = append(textParts, textPart.Text)
			}

		case "tool":
			if toolPart, ok := part.Content.(opencode.ToolPart); ok {
				toolDisplay := formatToolCall(toolPart, detailLevel, mutedStyle)
				if toolDisplay != "" {
					toolUses = append(toolUses, toolDisplay)
				}
			}

		case "step-start", "step-finish":
			// Skip step markers in normal display
			if detailLevel == "full" {
				if content, ok := part.Content.(map[string]interface{}); ok {
					if reason, ok := content["reason"].(string); ok {
						toolUses = append(toolUses, mutedStyle.Render(fmt.Sprintf("[Step: %s]", reason)))
					}
				}
			}
		}
	}

	// Display tool uses
	if len(toolUses) > 0 {
		role := robotStyle.Render(theme.IconRobot)
		for _, toolUse := range toolUses {
			ulogOpenCode.Info("Tool use").
				Field("role", entry.Role).
				Pretty(fmt.Sprintf("%s %s\n", role, toolUse)).
				PrettyOnly().
				Emit()
		}
		if len(textParts) > 0 {
			ulogOpenCode.Info("Tool text separator").
				Pretty("\n").
				PrettyOnly().
				Emit()
		}
	}

	// Display text content
	if len(textParts) > 0 {
		var role string
		if entry.Role == "assistant" {
			role = robotStyle.Render(theme.IconRobot)
		} else if entry.Role == "user" {
			role = userStyle.Render(theme.IconLightbulb)
		}
		ulogOpenCode.Info("Text content").
			Field("role", entry.Role).
			Pretty(fmt.Sprintf("%s %s\n\n", role, strings.Join(textParts, "\n"))).
			PrettyOnly().
			Emit()
	}
}

// formatToolCall formats a single tool call for display.
func formatToolCall(tool opencode.ToolPart, detailLevel string, mutedStyle lipgloss.Style) string {
	if detailLevel == "full" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("▼ %s", tool.Tool))
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
			// Truncate long diffs
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
	toolInfo := fmt.Sprintf("[Using %s", tool.Tool)

	// Show key input parameters
	if filePath, ok := tool.Input["filePath"].(string); ok {
		toolInfo += fmt.Sprintf(" on %s", filePath)
	} else if filePath, ok := tool.Input["file_path"].(string); ok {
		toolInfo += fmt.Sprintf(" on %s", filePath)
	} else if command, ok := tool.Input["command"].(string); ok {
		// Truncate long commands
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

// DisplayOpenCodeTranscript displays a full OpenCode transcript.
func DisplayOpenCodeTranscript(entries []opencode.TranscriptEntry, detailLevel string) {
	for _, entry := range entries {
		DisplayOpenCodeEntry(entry, detailLevel)
	}
}
