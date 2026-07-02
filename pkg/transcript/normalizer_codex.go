package transcript

import (
	"encoding/json"
	"strings"
	"time"
)

// CodexNormalizer normalizes Codex transcript entries.
type CodexNormalizer struct{}

// NewCodexNormalizer creates a new Codex normalizer.
func NewCodexNormalizer() *CodexNormalizer {
	return &CodexNormalizer{}
}

// Provider returns the provider name.
func (n *CodexNormalizer) Provider() string {
	return "codex"
}

// NormalizeLine normalizes a single Codex JSONL line to a UnifiedEntry.
func (n *CodexNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	payload, ok := raw["payload"].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	// Check top-level type first (response_item, event_msg, etc.)
	topLevelType, _ := raw["type"].(string)
	entryType, _ := payload["type"].(string)

	entry := &UnifiedEntry{
		Provider: "codex",
		Parts:    []UnifiedPart{},
	}

	// Extract timestamp if available
	if ts, ok := raw["timestamp"].(string); ok {
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	}

	// Handle event_msg types (agent_reasoning, agent_message, token_count)
	if topLevelType == "event_msg" {
		switch entryType {
		case "token_count":
			// Codex reports usage on a dedicated end-of-turn event rather
			// than on the message itself. Emit a parts-less entry carrying
			// the last turn's usage; renderers skip entries without parts,
			// while JSON/stream consumers get the token figures.
			tc, ok := ParseCodexTokenCountLine(line)
			if !ok {
				return nil, nil
			}
			entry.Role = "assistant"
			tokens := tc.Last
			entry.Tokens = &tokens
			return entry, nil
		case "agent_reasoning":
			entry.Role = "assistant"
			if text, ok := payload["text"].(string); ok {
				entry.Parts = append(entry.Parts, UnifiedPart{
					Type:    "reasoning",
					Content: UnifiedReasoning{Text: text},
				})
			}
		case "agent_message":
			entry.Role = "assistant"
			if message, ok := payload["message"].(string); ok {
				entry.Parts = append(entry.Parts, UnifiedPart{
					Type:    "text",
					Content: UnifiedTextContent{Text: message},
				})
			}
		default:
			return nil, nil
		}

		if len(entry.Parts) == 0 {
			return nil, nil
		}
		return entry, nil
	}

	// Handle response_item types
	if topLevelType == "response_item" {
		switch entryType {
		case "message":
			role, _ := payload["role"].(string)
			entry.Role = role
			if role == "" {
				entry.Role = "user"
			}

			// Skip assistant messages from response_item - we get these from event_msg/agent_message
			if role == "assistant" {
				return nil, nil
			}

			// Extract text content from content array
			if contentList, ok := payload["content"].([]interface{}); ok {
				for _, c := range contentList {
					if cMap, ok := c.(map[string]interface{}); ok {
						cType, _ := cMap["type"].(string)
						if cType == "input_text" || cType == "output_text" {
							if text, ok := cMap["text"].(string); ok && text != "" {
								// Skip environment_context messages
								if strings.Contains(text, "<environment_context>") {
									return nil, nil
								}
								entry.Parts = append(entry.Parts, UnifiedPart{
									Type:    "text",
									Content: UnifiedTextContent{Text: text},
								})
							}
						}
					}
				}
			}

		case "function_call":
			entry.Role = "assistant"
			name, _ := payload["name"].(string)
			argsStr, _ := payload["arguments"].(string)
			callID, _ := payload["call_id"].(string)

			// Preserve the full arguments object. Codex serializes function
			// call arguments as a JSON string (codex-rs/protocol/src/models.rs
			// ResponseItem::FunctionCall); parse it into a map so every key
			// survives — shell calls keep command/workdir/timeout_ms, non-shell
			// tools keep their whole input. If the string isn't valid JSON,
			// keep it raw under "arguments" rather than dropping it.
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil || args == nil {
				args = map[string]interface{}{}
				if argsStr != "" {
					args["arguments"] = argsStr
				}
			}

			entry.Parts = append(entry.Parts, UnifiedPart{
				Type: "tool_call",
				Content: UnifiedToolCall{
					ID:    callID,
					Name:  name,
					Input: args,
				},
			})

		case "function_call_output":
			entry.Role = "assistant"
			callID, _ := payload["call_id"].(string)
			outputStr, _ := payload["output"].(string)

			// Parse the output JSON
			var outputData struct {
				Output   string `json:"output"`
				Metadata struct {
					ExitCode        int     `json:"exit_code"`
					DurationSeconds float64 `json:"duration_seconds"`
				} `json:"metadata"`
			}
			_ = json.Unmarshal([]byte(outputStr), &outputData)

			isError := outputData.Metadata.ExitCode != 0

			entry.Parts = append(entry.Parts, UnifiedPart{
				Type: "tool_result",
				Content: UnifiedToolResult{
					ToolCallID: callID,
					Output:     outputData.Output,
					IsError:    isError,
				},
			})

		default:
			return nil, nil
		}

		if len(entry.Parts) == 0 {
			return nil, nil
		}
		return entry, nil
	}

	return nil, nil
}
