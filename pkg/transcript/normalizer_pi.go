package transcript

import (
	"encoding/json"
	"io"
	"time"
)

// PiNormalizer normalizes pi coding agent session entries
// (github.com/earendil-works/pi, session JSONL v3).
//
// pi session files are append-only TREES: every entry carries id/parentId,
// and editing/retrying inside a session moves the leaf pointer back to an
// earlier entry so the next append starts a new branch
// (packages/coding-agent/src/core/session-manager.ts). Naive line order is
// therefore wrong after any branching — abandoned branches would interleave
// with the active conversation. Whole-file reads must go through
// NormalizePiFile, which linearizes the tree exactly the way pi rebuilds its
// own context: the active leaf is the LAST entry in the file (pi's
// _buildIndex advances leafId on every appended entry), walk parentId from
// leaf to root, then emit root -> leaf.
//
// NormalizeLine (the streaming interface) normalizes single lines in append
// order, which is correct for live tailing: appends always extend the
// currently-active branch.
type PiNormalizer struct{}

// NewPiNormalizer creates a new pi normalizer.
func NewPiNormalizer() *PiNormalizer {
	return &PiNormalizer{}
}

// Provider returns the provider name.
func (n *PiNormalizer) Provider() string {
	return "pi"
}

// piFileEntry is one line of a pi session file: either the session header
// (type "session") or a SessionEntry with id/parentId tree pointers.
type piFileEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	// type == "message"
	Message json.RawMessage `json:"message"`
	// type == "custom_message" (extension-injected context, string or blocks)
	Content json.RawMessage `json:"content"`
	Display *bool           `json:"display"`
	// type == "custom": extension state persistence. The persisted entry type
	// is always "custom"; the writer controls only CustomType, and the payload
	// rides in Data (CustomEntry, packages/coding-agent/src/core/
	// session-manager.ts: { type: "custom", customType: string, data?: T }).
	// There is no Content and no Display on a custom entry — those belong to
	// the distinct custom_message type above.
	//
	// custom entries do NOT participate in the LLM context
	// (sessionEntryToContextMessages returns [] for them), which is exactly why
	// grove stamps its config vector as one: it persists without perturbing the
	// prompt bytes. They are correspondingly invisible to the rendered
	// transcript — normalizePiEntry must keep returning nil for them.
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`
}

// piMessage is the AgentMessage payload of a "message" entry
// (packages/ai/src/types.ts: UserMessage | AssistantMessage |
// ToolResultMessage).
type piMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string (user) or []piContentBlock
	// assistant
	Usage      *piUsage `json:"usage"`
	Model      string   `json:"model"`
	StopReason string   `json:"stopReason"`
	// toolResult
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
}

// piContentBlock is one element of a message content array: text, thinking,
// toolCall, or image.
type piContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text"`
	// thinking
	Thinking string `json:"thinking"`
	// toolCall
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// piUsage mirrors pi's Usage shape (packages/ai/src/types.ts). Token fields
// are already split: input excludes cacheRead/cacheWrite; reasoning is a
// subset of output. cost is a per-message dollar breakdown — cost.total is
// authoritative, no pricing-table lookup needed.
type piUsage struct {
	Input      int  `json:"input"`
	Output     int  `json:"output"`
	CacheRead  int  `json:"cacheRead"`
	CacheWrite int  `json:"cacheWrite"`
	Reasoning  *int `json:"reasoning"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// NormalizeLine normalizes a single pi JSONL line to a UnifiedEntry.
// Lines that don't contribute to the rendered conversation (header,
// model/thinking changes, labels, compaction bookkeeping, custom extension
// state) yield (nil, nil).
func (n *PiNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	var raw piFileEntry
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	return normalizePiEntry(&raw), nil
}

// normalizePiEntry converts a parsed pi file entry to a UnifiedEntry
// (nil when the entry type doesn't participate in the conversation).
func normalizePiEntry(raw *piFileEntry) *UnifiedEntry {
	switch raw.Type {
	case "message":
		return normalizePiMessage(raw)
	case "custom_message":
		// Extension-injected context; participates in the LLM context as a
		// user message (buildSessionContext in the pi source). display:false
		// means "hidden entirely" in pi's own TUI — honor that.
		if raw.Display != nil && !*raw.Display {
			return nil
		}
		entry := newPiUnifiedEntry(raw, "user")
		for _, part := range piTextParts(raw.Content) {
			entry.Parts = append(entry.Parts, part)
		}
		if len(entry.Parts) == 0 {
			return nil
		}
		return entry
	default:
		// "session" header, thinking_level_change, model_change, compaction,
		// branch_summary, custom, label, session_info: no conversation parts.
		return nil
	}
}

func normalizePiMessage(raw *piFileEntry) *UnifiedEntry {
	var msg piMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil
	}

	switch msg.Role {
	case "user":
		entry := newPiUnifiedEntry(raw, "user")
		for _, part := range piTextParts(msg.Content) {
			entry.Parts = append(entry.Parts, part)
		}
		if len(entry.Parts) == 0 {
			return nil
		}
		return entry

	case "assistant":
		entry := newPiUnifiedEntry(raw, "assistant")
		var blocks []piContentBlock
		_ = json.Unmarshal(msg.Content, &blocks)
		for _, b := range blocks {
			switch b.Type {
			case "thinking":
				if b.Thinking != "" {
					entry.Parts = append(entry.Parts, UnifiedPart{
						Type:    "reasoning",
						Content: UnifiedReasoning{Text: b.Thinking},
					})
				}
			case "text":
				if b.Text != "" {
					entry.Parts = append(entry.Parts, UnifiedPart{
						Type:    "text",
						Content: UnifiedTextContent{Text: b.Text},
					})
				}
			case "toolCall":
				entry.Parts = append(entry.Parts, UnifiedPart{
					Type: "tool_call",
					Content: UnifiedToolCall{
						ID:    b.ID,
						Name:  b.Name,
						Input: b.Arguments,
					},
				})
			}
		}
		if msg.Usage != nil {
			tokens := UnifiedTokens{
				Input:      msg.Usage.Input,
				Output:     msg.Usage.Output,
				CacheRead:  msg.Usage.CacheRead,
				CacheWrite: msg.Usage.CacheWrite,
				Cost:       msg.Usage.Cost.Total,
			}
			if msg.Usage.Reasoning != nil {
				tokens.Reasoning = *msg.Usage.Reasoning
			}
			entry.Tokens = &tokens
		}
		if len(entry.Parts) == 0 && entry.Tokens == nil {
			return nil
		}
		return entry

	case "toolResult":
		entry := newPiUnifiedEntry(raw, "assistant")
		output := ""
		for _, part := range piTextParts(msg.Content) {
			if tc, ok := part.Content.(UnifiedTextContent); ok {
				if output != "" {
					output += "\n"
				}
				output += tc.Text
			}
		}
		entry.Parts = append(entry.Parts, UnifiedPart{
			Type: "tool_result",
			Content: UnifiedToolResult{
				ToolCallID: msg.ToolCallID,
				Output:     output,
				IsError:    msg.IsError,
			},
		})
		return entry
	}

	return nil
}

// newPiUnifiedEntry builds the common envelope for a pi entry.
func newPiUnifiedEntry(raw *piFileEntry, role string) *UnifiedEntry {
	entry := &UnifiedEntry{
		Provider:  "pi",
		Role:      role,
		MessageID: raw.ID,
		Parts:     []UnifiedPart{},
	}
	if raw.Timestamp != "" {
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
	}
	return entry
}

// piTextParts extracts text parts from a pi content payload, which is either
// a plain string or an array of content blocks (text/image; images are
// skipped).
func piTextParts(content json.RawMessage) []UnifiedPart {
	if len(content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		if s == "" {
			return nil
		}
		return []UnifiedPart{{Type: "text", Content: UnifiedTextContent{Text: s}}}
	}
	var blocks []piContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var parts []UnifiedPart
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, UnifiedPart{Type: "text", Content: UnifiedTextContent{Text: b.Text}})
		}
	}
	return parts
}

// NormalizePiFile reads a complete pi session JSONL stream, linearizes the
// entry tree along the active branch, and normalizes it to UnifiedEntries in
// conversation order (root -> active leaf).
//
// Leaf selection matches pi's own resume behavior: SessionManager._buildIndex
// walks the file in order and leaves leafId at the LAST entry, so the active
// path is "last line, then follow parentId to the root". Entries on abandoned
// branches are dropped, exactly like pi drops them when rebuilding context.
// It is a thin wrapper over ParsePiSessionTree: parse the tree, take its
// active path, normalize it. The tree API is the general form (a file can hold
// several branches); this is the one question the renderer asks of it. Rendered
// output is regression-pinned by the golden fixtures in pi_tree_test.go, which
// were captured from the pre-refactor implementation.
func NormalizePiFile(r io.Reader) ([]UnifiedEntry, error) {
	tree, err := ParsePiSessionTree(r)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		return nil, nil
	}
	return tree.Normalize(tree.ActivePath()), nil
}
