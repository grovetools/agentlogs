// Package transcript provides unified transcript types across all providers.
package transcript

import (
	"time"
)

// UnifiedEntry represents a single transcript entry normalized across all providers.
type UnifiedEntry struct {
	Role        string         `json:"role"` // "user" or "assistant"
	Timestamp   time.Time      `json:"timestamp"`
	MessageID   string         `json:"messageID"`
	Parts       []UnifiedPart  `json:"parts"`
	Tokens      *UnifiedTokens `json:"tokens,omitempty"`
	Provider    string         `json:"provider"`              // "claude", "codex", "opencode", "journal"
	AgentID     string         `json:"agentID,omitempty"`     // Subagent ID for sidechain/workflow transcripts
	IsSidechain bool           `json:"isSidechain,omitempty"` // True for subagent (sidechain) entries
	PromptID    string         `json:"promptID,omitempty"`    // Prompt ID linking sidechain entries to their spawning prompt
}

// UnifiedPart represents a component of a message.
type UnifiedPart struct {
	Type    string      `json:"type"` // "text", "tool_call", "tool_result", "reasoning"
	Content interface{} `json:"content"`
}

// UnifiedTextContent holds text content.
type UnifiedTextContent struct {
	Text string `json:"text"`
}

// UnifiedToolCall holds tool invocation details.
type UnifiedToolCall struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Input  map[string]interface{} `json:"input"`
	Status string                 `json:"status,omitempty"` // For OpenCode: "pending", "completed", etc.
	Output string                 `json:"output,omitempty"`
	Title  string                 `json:"title,omitempty"`
	Diff   string                 `json:"diff,omitempty"`
}

// UnifiedToolResult holds tool execution results.
type UnifiedToolResult struct {
	ToolCallID string `json:"toolCallID"`
	Output     string `json:"output"`
	IsError    bool   `json:"isError,omitempty"`
}

// UnifiedReasoning holds reasoning/thinking content (Codex agent_reasoning).
type UnifiedReasoning struct {
	Text string `json:"text"`
}

// UnifiedTokens captures token usage across providers.
type UnifiedTokens struct {
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
	// Cost is the provider-reported dollar cost for this message, when the
	// provider computes it natively (pi: usage.cost.total per assistant
	// message). 0 means "not reported" — consumers needing cost for other
	// providers must use a pricing table instead.
	Cost float64 `json:"cost,omitempty"`
}
