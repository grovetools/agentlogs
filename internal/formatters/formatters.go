package formatters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// ToolFormatter is a function that formats the input of a tool call.
type ToolFormatter func(input json.RawMessage, detailLevel string) string

// stripCommonIndent removes common leading whitespace from all lines
func stripCommonIndent(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return text
	}

	// Find minimum indent (ignoring empty lines)
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return text
	}

	// Strip the common indent from all lines
	var result strings.Builder
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			result.WriteString("\n")
		} else {
			if len(line) >= minIndent {
				result.WriteString(line[minIndent:])
			} else {
				result.WriteString(line)
			}
			if i < len(lines)-1 {
				result.WriteString("\n")
			}
		}
	}
	return result.String()
}

// FormatWriteTool formats the input for Write or Edit tools, showing a diff-like view.
func FormatWriteTool(input json.RawMessage, maxLines int, detailLevel string) string {
	var data struct {
		FilePath  string `json:"file_path"`
		Content   string `json:"content"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return "" // Let the default formatter handle it
	}

	var output strings.Builder
	greenStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Green)
	redStyle := lipgloss.NewStyle().Foreground(theme.DefaultColors.Red)

	if data.OldString != "" && data.NewString != "" {
		// This is an Edit operation - show a clean diff
		output.WriteString(fmt.Sprintf("%s Editing %s\n", theme.IconFile, data.FilePath))

		// Strip common indentation before displaying
		oldStripped := stripCommonIndent(data.OldString)
		newStripped := stripCommonIndent(data.NewString)

		oldLines := strings.Split(oldStripped, "\n")
		newLines := strings.Split(newStripped, "\n")

		// Show diff content (0 means show all)
		linesToShow := len(oldLines)
		if maxLines > 0 && maxLines < linesToShow {
			linesToShow = maxLines
		}

		for i := 0; i < linesToShow; i++ {
			output.WriteString(redStyle.Render(fmt.Sprintf("  - %s", oldLines[i])) + "\n")
		}
		if len(oldLines) > linesToShow {
			output.WriteString(redStyle.Render(fmt.Sprintf("  - ... (%d more lines removed)", len(oldLines)-linesToShow)) + "\n")
		}

		// Show added content
		linesToShow = len(newLines)
		if maxLines > 0 && maxLines < linesToShow {
			linesToShow = maxLines
		}

		for i := 0; i < linesToShow; i++ {
			output.WriteString(greenStyle.Render(fmt.Sprintf("  + %s", newLines[i])) + "\n")
		}
		if len(newLines) > linesToShow {
			output.WriteString(greenStyle.Render(fmt.Sprintf("  + ... (%d more lines added)", len(newLines)-linesToShow)) + "\n")
		}
	} else if data.Content != "" {
		// This is a Write operation - just show we're writing to the file
		output.WriteString(fmt.Sprintf("%s Writing to %s\n", theme.IconFilePlus, data.FilePath))

		// Strip common indentation before displaying
		stripped := stripCommonIndent(data.Content)
		lines := strings.Split(stripped, "\n")

		if detailLevel == "full" || len(lines) <= 5 {
			for _, line := range lines {
				output.WriteString(greenStyle.Render(fmt.Sprintf("+ %s", line)) + "\n")
			}
		} else {
			output.WriteString(greenStyle.Render(fmt.Sprintf("+ (%d lines)", len(lines))) + "\n")
		}
	}

	return output.String()
}

// FormatReadTool formats the input for Read tool with minimal details.
func FormatReadTool(input json.RawMessage, detailLevel string) string {
	var data struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return ""
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%s Reading %s", theme.IconFile, data.FilePath))
	if data.Offset > 0 || data.Limit > 0 {
		output.WriteString(" (")
		if data.Offset > 0 {
			output.WriteString(fmt.Sprintf("offset: %d", data.Offset))
		}
		if data.Limit > 0 {
			if data.Offset > 0 {
				output.WriteString(", ")
			}
			output.WriteString(fmt.Sprintf("limit: %d", data.Limit))
		}
		output.WriteString(")")
	}
	output.WriteString("\n")
	return output.String()
}

// FormatTodoWriteTool formats the input for TodoWrite, showing a checklist.
func FormatTodoWriteTool(input json.RawMessage, detailLevel string) string {
	var data struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return ""
	}

	var checklist strings.Builder
	checklist.WriteString(fmt.Sprintf("%s TODO List Updated:\n", theme.IconChecklist))
	for _, item := range data.Todos {
		checkbox := "[ ]"
		if item.Status == "completed" {
			checkbox = "[✓]"
		} else if item.Status == "in_progress" {
			checkbox = "[→]"
		}
		checklist.WriteString(fmt.Sprintf("  %s %s\n", checkbox, item.Content))
	}
	return checklist.String()
}

// MakeWriteFormatter creates a Write formatter with the given max lines setting.
func MakeWriteFormatter(maxLines int) ToolFormatter {
	return func(input json.RawMessage, detailLevel string) string {
		return FormatWriteTool(input, maxLines, detailLevel)
	}
}
