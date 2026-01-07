package transcript

import (
	"github.com/mattsolo1/grove-agent-logs/internal/opencode"
)

// OpenCodeNormalizer normalizes OpenCode transcript entries.
type OpenCodeNormalizer struct{}

// NewOpenCodeNormalizer creates a new OpenCode normalizer.
func NewOpenCodeNormalizer() *OpenCodeNormalizer {
	return &OpenCodeNormalizer{}
}

// Provider returns the provider name.
func (n *OpenCodeNormalizer) Provider() string {
	return "opencode"
}

// NormalizeLine is not used for OpenCode as it uses assembled transcripts.
// OpenCode doesn't use line-by-line parsing; it uses the Assembler.
func (n *OpenCodeNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	return nil, nil // OpenCode uses NormalizeEntry instead
}

// NormalizeEntry converts an OpenCode TranscriptEntry to UnifiedEntry.
func (n *OpenCodeNormalizer) NormalizeEntry(oc opencode.TranscriptEntry) *UnifiedEntry {
	entry := &UnifiedEntry{
		Role:      oc.Role,
		Timestamp: oc.Timestamp,
		MessageID: oc.MessageID,
		Provider:  "opencode",
		Parts:     []UnifiedPart{},
	}

	// Convert token usage
	if oc.Tokens != nil {
		entry.Tokens = &UnifiedTokens{
			Input:      oc.Tokens.Input,
			Output:     oc.Tokens.Output,
			Reasoning:  oc.Tokens.Reasoning,
			CacheRead:  oc.Tokens.CacheRead,
			CacheWrite: oc.Tokens.CacheWrite,
		}
	}

	// Convert parts
	for _, part := range oc.Parts {
		switch part.Type {
		case "text":
			if textPart, ok := part.Content.(opencode.TextPart); ok && textPart.Text != "" {
				entry.Parts = append(entry.Parts, UnifiedPart{
					Type:    "text",
					Content: UnifiedTextContent{Text: textPart.Text},
				})
			}
		case "tool":
			if toolPart, ok := part.Content.(opencode.ToolPart); ok {
				entry.Parts = append(entry.Parts, UnifiedPart{
					Type: "tool_call",
					Content: UnifiedToolCall{
						ID:     toolPart.CallID,
						Name:   toolPart.Tool,
						Status: toolPart.Status,
						Input:  toolPart.Input,
						Output: toolPart.Output,
						Title:  toolPart.Title,
						Diff:   toolPart.Diff,
					},
				})
			}
		case "step-start", "step-finish":
			// Skip step markers in unified format (handled at display level if needed)
		}
	}

	return entry
}

// NormalizeAll converts a slice of OpenCode entries.
func (n *OpenCodeNormalizer) NormalizeAll(entries []opencode.TranscriptEntry) []UnifiedEntry {
	result := make([]UnifiedEntry, 0, len(entries))
	for _, e := range entries {
		if unified := n.NormalizeEntry(e); unified != nil && len(unified.Parts) > 0 {
			result = append(result, *unified)
		}
	}
	return result
}
