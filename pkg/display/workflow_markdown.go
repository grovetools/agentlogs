package display

import (
	"fmt"
	"strings"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// FormatWorkflowMarkdown converts a slice of UnifiedEntry into a clean Markdown document.
// This is the shared renderer used by both the TUI detail view and the disk archiver.
func FormatWorkflowMarkdown(entries []transcript.UnifiedEntry) string {
	var sb strings.Builder

	for i, entry := range entries {
		// Add separator between entries (except before first)
		if i > 0 {
			sb.WriteString("\n")
		}

		formatEntryMarkdown(&sb, entry)
	}

	return sb.String()
}

// formatEntryMarkdown formats a single UnifiedEntry to markdown.
func formatEntryMarkdown(sb *strings.Builder, entry transcript.UnifiedEntry) {
	switch entry.Role {
	case "user":
		formatUserEntryMarkdown(sb, entry)
	case "assistant":
		formatAssistantEntryMarkdown(sb, entry)
	}
}

// formatUserEntryMarkdown formats a user message entry.
func formatUserEntryMarkdown(sb *strings.Builder, entry transcript.UnifiedEntry) {
	sb.WriteString("## User\n\n")

	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			text := extractTextContent(part)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString("\n\n")
			}
		case "tool_result":
			formatToolResultMarkdown(sb, part)
		}
	}
}

// formatAssistantEntryMarkdown formats an assistant message entry.
func formatAssistantEntryMarkdown(sb *strings.Builder, entry transcript.UnifiedEntry) {
	sb.WriteString("## Assistant\n\n")

	for _, part := range entry.Parts {
		switch part.Type {
		case "text":
			text := extractTextContent(part)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString("\n\n")
			}
		case "tool_call":
			formatToolCallMarkdown(sb, part)
		case "tool_result":
			formatToolResultMarkdown(sb, part)
		case "reasoning":
			formatReasoningMarkdown(sb, part)
		}
	}
}

// formatToolCallMarkdown formats a tool call part.
func formatToolCallMarkdown(sb *strings.Builder, part transcript.UnifiedPart) {
	toolCall := extractToolCall(part)
	if toolCall.Name == "" {
		return
	}

	// Format as bold with key argument: **Tool Call:** ToolName(arg)
	keyArg := extractKeyArg(toolCall)
	toolName := capitalizeFirst(toolCall.Name)

	if keyArg != "" {
		sb.WriteString(fmt.Sprintf("**Tool Call:** %s(%s)\n\n", toolName, keyArg))
	} else if toolCall.Title != "" {
		sb.WriteString(fmt.Sprintf("**Tool Call:** %s(%s)\n\n", toolName, toolCall.Title))
	} else {
		sb.WriteString(fmt.Sprintf("**Tool Call:** %s\n\n", toolName))
	}

	// If there's embedded output (OpenCode or merged Claude), show it
	if toolCall.Output != "" {
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimSpace(toolCall.Output))
		sb.WriteString("\n```\n\n")
	}
}

// formatToolResultMarkdown formats a tool result part.
func formatToolResultMarkdown(sb *strings.Builder, part transcript.UnifiedPart) {
	result := extractToolResult(part)
	if result.Output == "" {
		return
	}

	// Show tool result in a code block
	sb.WriteString("```\n")
	sb.WriteString(strings.TrimSpace(result.Output))
	sb.WriteString("\n```\n\n")
}

// formatReasoningMarkdown formats a reasoning/thinking part.
func formatReasoningMarkdown(sb *strings.Builder, part transcript.UnifiedPart) {
	text := extractReasoningContent(part)
	if text == "" {
		return
	}

	// Format as blockquote with a header
	sb.WriteString("> *Thinking...*\n>\n")

	// Add each line as a blockquote line
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			sb.WriteString("> ")
			sb.WriteString(line)
			sb.WriteString("\n")
		} else {
			sb.WriteString(">\n")
		}
	}
	sb.WriteString("\n")
}

// extractTextContent extracts text from a part's content.
func extractTextContent(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedTextContent); ok {
		return content.Text
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		if text, ok := contentMap["text"].(string); ok {
			return text
		}
	}
	return ""
}

// extractToolCall extracts a UnifiedToolCall from a part's content.
func extractToolCall(part transcript.UnifiedPart) transcript.UnifiedToolCall {
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

// extractToolResult extracts a UnifiedToolResult from a part's content.
func extractToolResult(part transcript.UnifiedPart) transcript.UnifiedToolResult {
	if content, ok := part.Content.(transcript.UnifiedToolResult); ok {
		return content
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		return transcript.UnifiedToolResult{
			ToolCallID: getStringField(contentMap, "toolCallID"),
			Output:     getStringField(contentMap, "output"),
			IsError:    getBoolField(contentMap, "isError"),
		}
	}
	return transcript.UnifiedToolResult{}
}

// extractReasoningContent extracts reasoning text from a part's content.
func extractReasoningContent(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedReasoning); ok {
		return content.Text
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		return getStringField(contentMap, "text")
	}
	return ""
}

// getBoolField safely extracts a bool field from a map.
func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
