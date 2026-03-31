package transcript

import (
	"encoding/json"
	"time"
)

// ClaudeNormalizer normalizes Claude transcript entries.
// It maintains state to match tool_results back to their corresponding tool_calls.
type ClaudeNormalizer struct {
	// pendingToolCalls maps tool call IDs to their reference
	pendingToolCalls map[string]*pendingToolCallRef
	// pendingEntries accumulates assistant entries with tool calls waiting for results
	pendingEntries []*UnifiedEntry
}

// pendingToolCallRef tracks where a tool call is located
type pendingToolCallRef struct {
	entry     *UnifiedEntry // pointer to the entry containing this tool call
	partIndex int           // index into entry.Parts
}

// NewClaudeNormalizer creates a new Claude normalizer.
func NewClaudeNormalizer() *ClaudeNormalizer {
	return &ClaudeNormalizer{
		pendingToolCalls: make(map[string]*pendingToolCallRef),
		pendingEntries:   make([]*UnifiedEntry, 0),
	}
}

// Provider returns the provider name.
func (n *ClaudeNormalizer) Provider() string {
	return "claude"
}

// Flush returns any buffered entries that haven't been emitted yet.
// Call this after processing all lines to ensure no entries are lost.
func (n *ClaudeNormalizer) Flush() []*UnifiedEntry {
	if len(n.pendingEntries) > 0 {
		entries := n.pendingEntries
		n.pendingEntries = make([]*UnifiedEntry, 0)
		n.pendingToolCalls = make(map[string]*pendingToolCallRef)
		return entries
	}
	return nil
}

// NormalizeLine normalizes a single Claude JSONL line to a UnifiedEntry.
// It buffers assistant messages with tool calls and merges them with subsequent tool results.
// Returns nil when buffering; call Flush() at end to get remaining entries.
func (n *ClaudeNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	// Parse the raw entry structure
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
		return nil, nil
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

	// Handle assistant messages
	if raw.Type == "assistant" {
		// Check if this entry has tool calls
		hasToolCalls := false

		for i, part := range entry.Parts {
			if part.Type == "tool_call" {
				if tc, ok := part.Content.(UnifiedToolCall); ok && tc.ID != "" {
					n.pendingToolCalls[tc.ID] = &pendingToolCallRef{
						entry:     entry, // Store pointer directly
						partIndex: i,
					}
					hasToolCalls = true
				}
			}
		}

		if hasToolCalls {
			// Buffer this entry - we'll emit it when we get the tool results
			n.pendingEntries = append(n.pendingEntries, entry)
			return nil, nil // Don't emit yet
		}

		// No tool calls - emit immediately
		return entry, nil
	}

	// Handle user messages
	if raw.Type == "user" {
		// Check if this message has tool_results that match our pending tool calls
		if len(n.pendingEntries) > 0 && len(n.pendingToolCalls) > 0 {
			// Look for tool_result in this user message
			var entryToEmit *UnifiedEntry
			var textParts []UnifiedPart

			for _, part := range entry.Parts {
				if part.Type == "tool_result" {
					if tr, ok := part.Content.(UnifiedToolResult); ok && tr.ToolCallID != "" {
						// Find the matching tool call using pointer
						if ref, exists := n.pendingToolCalls[tr.ToolCallID]; exists {
							pendingEntry := ref.entry
							if ref.partIndex < len(pendingEntry.Parts) {
								if tc, ok := pendingEntry.Parts[ref.partIndex].Content.(UnifiedToolCall); ok {
									tc.Output = tr.Output
									pendingEntry.Parts[ref.partIndex].Content = tc
								}
							}
							// Mark this entry for emission
							entryToEmit = pendingEntry
							// Remove from pending
							delete(n.pendingToolCalls, tr.ToolCallID)
						}
					}
				} else {
					textParts = append(textParts, part)
				}
			}

			// If we found a matching entry, emit it
			if entryToEmit != nil {
				// Remove the entry from pendingEntries
				newPending := make([]*UnifiedEntry, 0, len(n.pendingEntries)-1)
				for _, e := range n.pendingEntries {
					if e != entryToEmit {
						newPending = append(newPending, e)
					}
				}
				n.pendingEntries = newPending
				return entryToEmit, nil
			}

			// If we have text content (actual user message, not just tool results), return it
			if len(textParts) > 0 {
				for _, part := range textParts {
					if part.Type == "text" {
						if tc, ok := part.Content.(UnifiedTextContent); ok && tc.Text != "" {
							return entry, nil
						}
					}
				}
			}

			return nil, nil // Tool result didn't match any pending call
		}

		// No pending tool calls - just return the user entry
		return entry, nil
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
