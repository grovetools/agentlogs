package display

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/grovetools/agentlogs/pkg/formatters"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// Formatting constants for output
const (
	treeChar = "⎿" // Tree connector for sub-content
)

// FormatUnifiedEntry renders a single UnifiedEntry to a string in terminal
// style.
//
// Deprecated: use RenderUnifiedEntry with a bytes.Buffer instead.
func FormatUnifiedEntry(
	entry transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) string {
	var buf bytes.Buffer
	_ = RenderUnifiedEntry(&buf, entry, RenderOptions{Style: StyleTerminal, DetailLevel: detailLevel}, toolFormatters)
	return strings.TrimRight(buf.String(), "\n")
}

// DefaultToolFormatters returns the standard set of tool formatters.
func DefaultToolFormatters() map[string]formatters.ToolFormatter {
	return map[string]formatters.ToolFormatter{
		"Write":     formatters.MakeWriteFormatter(0),
		"Edit":      formatters.MakeWriteFormatter(0),
		"Read":      formatters.FormatReadTool,
		"TodoWrite": formatters.FormatTodoWriteTool,
	}
}

// DisplayUnifiedEntry renders a single UnifiedEntry to stdout in terminal
// style. Thin wrapper over RenderUnifiedEntry.
func DisplayUnifiedEntry(
	entry transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	_ = RenderUnifiedEntry(os.Stdout, entry, RenderOptions{Style: StyleTerminal, DetailLevel: detailLevel}, toolFormatters)
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
func formatToolOutput(toolName, output string, mutedStyle lipgloss.Style) string {
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
// For Edit/Write tools, uses specialized formatters to show diffs.
func formatUnifiedToolCall(
	tool transcript.UnifiedToolCall,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
	mutedStyle lipgloss.Style,
) string {
	// Capitalize tool name for consistency
	toolName := capitalizeFirst(tool.Name)

	// Check if we have a specialized formatter for this tool
	if formatter, ok := toolFormatters[tool.Name]; ok {
		// Marshal the input back to JSON for the formatter
		if inputJSON, err := json.Marshal(tool.Input); err == nil {
			formatted := formatter(inputJSON, detailLevel)
			if formatted != "" {
				return strings.TrimSuffix(formatted, "\n")
			}
		}
	}

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
		return truncateCommand(cmd)
	}

	// Codex shell calls carry command as an argv array (["bash","-lc","cmd"]).
	if cmdArr, ok := tool.Input["command"].([]interface{}); ok && len(cmdArr) > 0 {
		if cmd := commandArrayString(cmdArr); cmd != "" {
			return truncateCommand(cmd)
		}
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

// truncateCommand trims and caps a shell command for inline display.
func truncateCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) > 60 {
		return cmd[:57] + "..."
	}
	return cmd
}

// commandArrayString renders a codex-style argv command array for display.
// The ["bash","-lc","actual command"] shape shows just the actual command;
// other shapes join the argv with spaces.
func commandArrayString(cmdArr []interface{}) string {
	if len(cmdArr) >= 3 {
		if flag, ok := cmdArr[1].(string); ok && (flag == "-lc" || flag == "-c") {
			if cmd, ok := cmdArr[2].(string); ok {
				return cmd
			}
		}
	}
	parts := make([]string, 0, len(cmdArr))
	for _, c := range cmdArr {
		if s, ok := c.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
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

// DisplayUnifiedTranscript displays a full transcript to stdout in terminal
// style. Thin wrapper over RenderUnifiedTranscript.
func DisplayUnifiedTranscript(
	entries []transcript.UnifiedEntry,
	detailLevel string,
	toolFormatters map[string]formatters.ToolFormatter,
) {
	_ = RenderUnifiedTranscript(os.Stdout, entries, RenderOptions{Style: StyleTerminal, DetailLevel: detailLevel}, toolFormatters)
}
