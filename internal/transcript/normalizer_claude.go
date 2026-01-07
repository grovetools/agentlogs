package transcript

import (
	"encoding/json"
	"time"
)

// ClaudeNormalizer normalizes Claude transcript entries.
type ClaudeNormalizer struct{}

// NewClaudeNormalizer creates a new Claude normalizer.
func NewClaudeNormalizer() *ClaudeNormalizer {
	return &ClaudeNormalizer{}
}

// Provider returns the provider name.
func (n *ClaudeNormalizer) Provider() string {
	return "claude"
}

// NormalizeLine normalizes a single Claude JSONL line to a UnifiedEntry.
func (n *ClaudeNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	// Parse the raw entry structure matching display.TranscriptEntry
	var raw struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		SessionID string          `json:"sessionId"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	// Only process user/assistant entries
	if raw.Type != "user" && raw.Type != "assistant" {
		return nil, nil // Skip non-message entries
	}

	entry := &UnifiedEntry{
		Role:      raw.Type,
		Timestamp: raw.Timestamp,
		Provider:  "claude",
		Parts:     []UnifiedPart{},
	}

	// Parse message content
	if raw.Message != nil {
		var msg struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &msg); err == nil {
			entry.MessageID = msg.ID
			entry.Parts = n.parseContent(msg.Content)
		}
	}

	return entry, nil
}

func (n *ClaudeNormalizer) parseContent(content json.RawMessage) []UnifiedPart {
	var parts []UnifiedPart

	// Try string content first (user messages)
	var strContent string
	if err := json.Unmarshal(content, &strContent); err == nil {
		if strContent != "" {
			parts = append(parts, UnifiedPart{
				Type:    "text",
				Content: UnifiedTextContent{Text: strContent},
			})
		}
		return parts
	}

	// Try array content (assistant messages with tool_use, text, tool_result)
	var contentArray []json.RawMessage
	if err := json.Unmarshal(content, &contentArray); err != nil {
		return parts
	}

	for _, rawItem := range contentArray {
		var item struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"` // Claude's extended thinking
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}

		switch item.Type {
		case "text":
			if item.Text != "" {
				parts = append(parts, UnifiedPart{
					Type:    "text",
					Content: UnifiedTextContent{Text: item.Text},
				})
			}
		case "thinking":
			// Claude's extended thinking - display as reasoning
			if item.Thinking != "" {
				parts = append(parts, UnifiedPart{
					Type:    "reasoning",
					Content: UnifiedReasoning{Text: item.Thinking},
				})
			}
		case "tool_use":
			var inputMap map[string]interface{}
			json.Unmarshal(item.Input, &inputMap)
			parts = append(parts, UnifiedPart{
				Type: "tool_call",
				Content: UnifiedToolCall{
					ID:    item.ID,
					Name:  item.Name,
					Input: inputMap,
				},
			})
		case "tool_result":
			var output string
			json.Unmarshal(item.Content, &output)
			parts = append(parts, UnifiedPart{
				Type: "tool_result",
				Content: UnifiedToolResult{
					ToolCallID: item.ToolUseID,
					Output:     output,
				},
			})
		}
	}

	return parts
}
