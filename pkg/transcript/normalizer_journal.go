package transcript

import (
	"encoding/json"
	"fmt"
)

// JournalNormalizer normalizes workflow journal.jsonl events.
//
// Journal events carry no timestamp. The two observed shapes are:
//
//	{"type":"started","key":"v2:<sha256>","agentId":"<id>"}
//	{"type":"result","key":"v2:<sha256>","agentId":"<id>","result":<arbitrary JSON>}
type JournalNormalizer struct{}

// NewJournalNormalizer creates a new journal normalizer.
func NewJournalNormalizer() *JournalNormalizer {
	return &JournalNormalizer{}
}

// Provider returns the provider name.
func (n *JournalNormalizer) Provider() string {
	return "journal"
}

// NormalizeLine normalizes a single journal.jsonl line to a UnifiedEntry.
// Unknown event types are skipped (returned as nil, nil) so journal format
// drift degrades gracefully instead of failing the stream.
func (n *JournalNormalizer) NormalizeLine(line []byte) (*UnifiedEntry, error) {
	var raw struct {
		Type    string          `json:"type"`
		Key     string          `json:"key"`
		AgentID string          `json:"agentId"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	if raw.Type != "started" && raw.Type != "result" {
		return nil, nil
	}

	entry := &UnifiedEntry{
		Role:        "system",
		Provider:    "journal",
		AgentID:     raw.AgentID,
		IsSidechain: true,
	}

	var text string
	switch raw.Type {
	case "started":
		text = fmt.Sprintf("workflow agent %s started (key %s)", raw.AgentID, raw.Key)
	case "result":
		text = fmt.Sprintf("workflow agent %s result (key %s)", raw.AgentID, raw.Key)
		if len(raw.Result) > 0 {
			text += "\n" + string(raw.Result)
		}
	}
	entry.Parts = []UnifiedPart{{
		Type:    "text",
		Content: UnifiedTextContent{Text: text},
	}}

	return entry, nil
}
