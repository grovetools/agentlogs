package transcript

import (
	"bufio"
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
func NormalizePiFile(r io.Reader) ([]UnifiedEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var order []*piFileEntry
	byID := make(map[string]*piFileEntry)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw piFileEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // tolerate torn/partial lines (live files)
		}
		if raw.Type == "session" || raw.ID == "" {
			continue
		}
		entry := raw
		order = append(order, &entry)
		byID[entry.ID] = &entry
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}

	// Walk leaf -> root, then reverse.
	var path []*piFileEntry
	seen := make(map[string]bool)
	for cur := order[len(order)-1]; cur != nil; {
		if seen[cur.ID] {
			break // cycle guard (malformed file)
		}
		seen[cur.ID] = true
		path = append(path, cur)
		if cur.ParentID == nil || *cur.ParentID == "" {
			break
		}
		cur = byID[*cur.ParentID]
	}

	entries := make([]UnifiedEntry, 0, len(path))
	for i := len(path) - 1; i >= 0; i-- {
		if entry := normalizePiEntry(path[i]); entry != nil {
			entries = append(entries, *entry)
		}
	}
	return entries, nil
}
