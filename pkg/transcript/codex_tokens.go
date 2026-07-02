package transcript

import "encoding/json"

// codexTokenUsage mirrors codex-rs's TokenUsage struct
// (codex-rs/protocol/src/protocol.rs). Note that input_tokens INCLUDES
// cached_input_tokens (OpenAI prompt-token semantics), unlike Claude's usage
// shape where input and cache_read are disjoint.
type codexTokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

// toUnified converts codex usage to the provider-neutral UnifiedTokens shape,
// splitting the cached portion out of input so Input/CacheRead are disjoint
// like the other providers.
func (u codexTokenUsage) toUnified() UnifiedTokens {
	fresh := u.InputTokens - u.CachedInputTokens
	if fresh < 0 {
		fresh = u.InputTokens
	}
	return UnifiedTokens{
		Input:     fresh,
		Output:    u.OutputTokens,
		Reasoning: u.ReasoningOutputTokens,
		CacheRead: u.CachedInputTokens,
	}
}

// CodexTokenCount is the token usage carried by one codex token_count event.
// Codex emits these as event_msg rollout lines after each turn
// (codex-rs/rollout/src/policy.rs persists EventMsg::TokenCount).
type CodexTokenCount struct {
	// Last is the usage of the most recent turn (codex last_token_usage).
	Last UnifiedTokens
	// Total is the cumulative usage for the session (codex total_token_usage).
	Total UnifiedTokens
	// ModelContextWindow is the model's context window when reported (0 = unknown).
	ModelContextWindow int
}

// codexTokenCountLine is the subset of a codex rollout JSONL line needed for
// token accounting:
//
//	{"timestamp":"...","type":"event_msg","payload":{"type":"token_count",
//	  "info":{"total_token_usage":{...},"last_token_usage":{...},
//	          "model_context_window":N},"rate_limits":...}}
//
// Older codex versions serialized the TokenUsage fields flat on the payload
// itself; both shapes are handled.
type codexTokenCountLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
			LastTokenUsage     codexTokenUsage `json:"last_token_usage"`
			ModelContextWindow int             `json:"model_context_window"`
		} `json:"info"`
		codexTokenUsage // legacy flat shape
	} `json:"payload"`
}

// ParseCodexTokenCountLine parses one codex rollout JSONL line and returns the
// token usage when the line is a token_count event carrying usage info. The
// second return value is false for any other line (including token_count
// events with a null info, which codex emits for rate-limit-only updates).
func ParseCodexTokenCountLine(line []byte) (CodexTokenCount, bool) {
	var raw codexTokenCountLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return CodexTokenCount{}, false
	}
	if raw.Type != "event_msg" || raw.Payload.Type != "token_count" {
		return CodexTokenCount{}, false
	}
	if raw.Payload.Info != nil {
		return CodexTokenCount{
			Last:               raw.Payload.Info.LastTokenUsage.toUnified(),
			Total:              raw.Payload.Info.TotalTokenUsage.toUnified(),
			ModelContextWindow: raw.Payload.Info.ModelContextWindow,
		}, true
	}
	// Legacy flat shape: the TokenUsage fields sit directly on the payload.
	// Treat it as per-turn usage (older codex reported one usage per turn).
	if raw.Payload.codexTokenUsage == (codexTokenUsage{}) {
		return CodexTokenCount{}, false
	}
	u := raw.Payload.codexTokenUsage.toUnified()
	return CodexTokenCount{Last: u, Total: u}, true
}
