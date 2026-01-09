package display

import (
	"encoding/json"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
)

var ulogDisplay = grovelogging.NewUnifiedLogger("grove-agent-logs.display.legacy")

// TranscriptEntry represents a single entry in the transcript.
type TranscriptEntry struct {
	Type    string           `json:"type"`
	Message *json.RawMessage `json:"message"`
}

// DisplayTranscriptEntry displays a single transcript entry with proper formatting.
func DisplayTranscriptEntry(
	entry TranscriptEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	// Parse the message
	if entry.Message == nil {
		return
	}

	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(*entry.Message, &msg); err != nil {
		return
	}
	// Extract message content if it's a user or assistant message
	if entry.Type == "user" || entry.Type == "assistant" {
		// Handle both string and array content formats
		var textContent string
		var toolUses []string

		mutedStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.MutedText)

		// Try string content first (for user messages)
		var stringContent string
		if err := json.Unmarshal(msg.Content, &stringContent); err == nil {
			textContent = stringContent
		} else {
			// Try array content (for assistant messages and user tool results)
			var contentArray []json.RawMessage
			if err := json.Unmarshal(msg.Content, &contentArray); err == nil {
				for _, rawContent := range contentArray {
					var content struct {
						Type      string          `json:"type"`
						Text      string          `json:"text"`
						Name      string          `json:"name"`
						Input     json.RawMessage `json:"input"`
						ToolUseID string          `json:"tool_use_id"`
						Content   json.RawMessage `json:"content"` // For tool_result
					}
					if err := json.Unmarshal(rawContent, &content); err == nil {
						if content.Type == "text" {
							if textContent != "" {
								textContent += "\n"
							}
							textContent += content.Text
						} else if content.Type == "tool_use" {
							// Check for a specialized formatter first
							if formatter, ok := toolFormatters[content.Name]; ok {
								formattedOutput := formatter(content.Input, detailLevel)
								if formattedOutput != "" {
									toolUses = append(toolUses, formattedOutput)
									continue // Skip generic summary and detailed JSON if formatter was used
								}
							}

							// Display detailed tool input if requested (only for tools without specialized formatters)
							if detailLevel == "full" {
								var prettyInput []byte
								prettyInput, err := json.MarshalIndent(content.Input, "", "  ")
								if err == nil {
									toolInputStr := fmt.Sprintf("▼ Input for %s:\n%s", content.Name, string(prettyInput))
									toolUses = append(toolUses, mutedStyle.Render(toolInputStr))
								} else {
									toolUses = append(toolUses, mutedStyle.Render(fmt.Sprintf("▼ Input for %s (raw):\n%s", content.Name, string(content.Input))))
								}
							} else {
								// Summary mode - show tool name and key inputs
								toolInfo := fmt.Sprintf("[Using %s", content.Name)

								// Try to extract common input fields
								var inputs map[string]interface{}
								if err := json.Unmarshal(content.Input, &inputs); err == nil {
									// Show file paths, commands, or other key parameters
									if filePath, ok := inputs["file_path"].(string); ok {
										toolInfo += fmt.Sprintf(" on %s", filePath)
									} else if command, ok := inputs["command"].(string); ok {
										// Truncate long commands
										if len(command) > 50 {
											toolInfo += fmt.Sprintf(": %s...", command[:50])
										} else {
											toolInfo += fmt.Sprintf(": %s", command)
										}
									} else if pattern, ok := inputs["pattern"].(string); ok {
										toolInfo += fmt.Sprintf(" for '%s'", pattern)
									}
								}
								toolInfo += "]"
								toolUses = append(toolUses, toolInfo)
							}
						} else if content.Type == "tool_result" {
							// Display tool output if requested (skip for now to reduce noise)
							// Tool results are typically shown by the system or are verbose
							// Users can check actual log files for full output details
						}
					}
				}
			}
		}

		// Display tool uses if any
		if len(toolUses) > 0 {
			robotStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Violet)
			role := robotStyle.Render(theme.IconRobot)
			for _, toolUse := range toolUses {
				ulogDisplay.Info("Tool use").
					Field("entry_type", entry.Type).
					Pretty(fmt.Sprintf("%s %s\n", role, toolUse)).
					PrettyOnly().
					Emit()
			}
			if textContent != "" {
				ulogDisplay.Info("Tool text separator").
					Pretty("\n").
					PrettyOnly().
					Emit()
			}
		}

		// Display text content
		if textContent != "" {
			var role string
			if entry.Type == "assistant" {
				robotStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Violet)
				role = robotStyle.Render(theme.IconRobot)
			} else if entry.Type == "user" {
				userStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Yellow)
				role = userStyle.Render(theme.IconLightbulb)
			}
			ulogDisplay.Info("Text content").
				Field("entry_type", entry.Type).
				Pretty(fmt.Sprintf("%s %s\n\n", role, textContent)).
				PrettyOnly().
				Emit()
		}
	}
}
