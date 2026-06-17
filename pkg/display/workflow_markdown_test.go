package display

import (
	"strings"
	"testing"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

func TestFormatWorkflowMarkdown_UserTextPart(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "user",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: transcript.UnifiedTextContent{Text: "Hello, can you help me?"},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "## User") {
		t.Errorf("expected '## User' header, got: %s", result)
	}
	if !strings.Contains(result, "Hello, can you help me?") {
		t.Errorf("expected user text content, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_AssistantTextPart(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: transcript.UnifiedTextContent{Text: "I can help you with that!"},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "## Assistant") {
		t.Errorf("expected '## Assistant' header, got: %s", result)
	}
	if !strings.Contains(result, "I can help you with that!") {
		t.Errorf("expected assistant text content, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_ToolCall(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "tool_call",
					Content: transcript.UnifiedToolCall{
						ID:    "tool_123",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "/path/to/file.go"},
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "**Tool Call:**") {
		t.Errorf("expected '**Tool Call:**' marker, got: %s", result)
	}
	if !strings.Contains(result, "Read") {
		t.Errorf("expected tool name 'Read', got: %s", result)
	}
	if !strings.Contains(result, "/path/to/file.go") {
		t.Errorf("expected file path in tool call, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_ToolCallWithCommand(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "tool_call",
					Content: transcript.UnifiedToolCall{
						ID:    "tool_456",
						Name:  "Bash",
						Input: map[string]interface{}{"command": "go build ./..."},
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "Bash") {
		t.Errorf("expected tool name 'Bash', got: %s", result)
	}
	if !strings.Contains(result, "go build ./...") {
		t.Errorf("expected command in tool call, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_ToolResult(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "user",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "tool_result",
					Content: transcript.UnifiedToolResult{
						ToolCallID: "tool_123",
						Output:     "File contents here\nLine 2",
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "```") {
		t.Errorf("expected code block markers, got: %s", result)
	}
	if !strings.Contains(result, "File contents here") {
		t.Errorf("expected tool result output, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_Reasoning(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "reasoning",
					Content: transcript.UnifiedReasoning{
						Text: "Let me think about this...\nI should check the file first.",
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "> *Thinking...*") {
		t.Errorf("expected thinking header, got: %s", result)
	}
	if !strings.Contains(result, "> Let me think about this...") {
		t.Errorf("expected reasoning content as blockquote, got: %s", result)
	}
	if !strings.Contains(result, "> I should check the file first.") {
		t.Errorf("expected multi-line reasoning as blockquotes, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_MultipleEntries(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "user",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: transcript.UnifiedTextContent{Text: "First message"},
				},
			},
		},
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: transcript.UnifiedTextContent{Text: "Response"},
				},
			},
		},
		{
			Role:      "user",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: transcript.UnifiedTextContent{Text: "Follow up"},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	// Should have 2 User headers and 1 Assistant header
	if strings.Count(result, "## User") != 2 {
		t.Errorf("expected 2 '## User' headers, got: %d in %s", strings.Count(result, "## User"), result)
	}
	if strings.Count(result, "## Assistant") != 1 {
		t.Errorf("expected 1 '## Assistant' header, got: %d in %s", strings.Count(result, "## Assistant"), result)
	}
}

func TestFormatWorkflowMarkdown_MapContent(t *testing.T) {
	// Test content as map[string]interface{} (how JSON unmarshals)
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type:    "text",
					Content: map[string]interface{}{"text": "Text from map"},
				},
				{
					Type: "tool_call",
					Content: map[string]interface{}{
						"id":    "tool_789",
						"name":  "Grep",
						"input": map[string]interface{}{"pattern": "func main"},
					},
				},
				{
					Type: "tool_result",
					Content: map[string]interface{}{
						"toolCallID": "tool_789",
						"output":     "main.go:10: func main() {",
					},
				},
				{
					Type:    "reasoning",
					Content: map[string]interface{}{"text": "Thinking from map"},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	if !strings.Contains(result, "Text from map") {
		t.Errorf("expected text from map content, got: %s", result)
	}
	if !strings.Contains(result, "Grep") {
		t.Errorf("expected tool name from map content, got: %s", result)
	}
	if !strings.Contains(result, "func main") {
		t.Errorf("expected pattern arg from map content, got: %s", result)
	}
	if !strings.Contains(result, "main.go:10") {
		t.Errorf("expected tool result from map content, got: %s", result)
	}
	if !strings.Contains(result, "Thinking from map") {
		t.Errorf("expected reasoning from map content, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_ToolCallWithEmbeddedOutput(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "tool_call",
					Content: transcript.UnifiedToolCall{
						ID:     "tool_abc",
						Name:   "Read",
						Input:  map[string]interface{}{"file_path": "/path/to/file.go"},
						Output: "package main\n\nfunc main() {}",
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	// Should have tool call header
	if !strings.Contains(result, "**Tool Call:** Read") {
		t.Errorf("expected tool call header, got: %s", result)
	}
	// Should have embedded output in code block
	if !strings.Contains(result, "package main") {
		t.Errorf("expected embedded output, got: %s", result)
	}
	// Code block count: should be 1 for embedded output
	if strings.Count(result, "```") != 2 { // opening and closing
		t.Errorf("expected 2 code block markers (1 block), got: %d in %s", strings.Count(result, "```"), result)
	}
}

func TestFormatWorkflowMarkdown_EmptyEntries(t *testing.T) {
	result := FormatWorkflowMarkdown(nil)
	if result != "" {
		t.Errorf("expected empty string for nil entries, got: %s", result)
	}

	result = FormatWorkflowMarkdown([]transcript.UnifiedEntry{})
	if result != "" {
		t.Errorf("expected empty string for empty entries, got: %s", result)
	}
}

func TestFormatWorkflowMarkdown_ToolCallCapitalization(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{
			Role:      "assistant",
			Timestamp: time.Now(),
			Parts: []transcript.UnifiedPart{
				{
					Type: "tool_call",
					Content: transcript.UnifiedToolCall{
						ID:    "tool_cap",
						Name:  "webFetch", // lowercase first char
						Input: map[string]interface{}{"url": "https://example.com"},
					},
				},
			},
		},
	}

	result := FormatWorkflowMarkdown(entries)

	// Should capitalize the tool name
	if !strings.Contains(result, "WebFetch") {
		t.Errorf("expected capitalized tool name 'WebFetch', got: %s", result)
	}
}
