// Package opencode provides functionality for reading and assembling OpenCode transcripts.
package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/logging"
	"github.com/sirupsen/logrus"
)

// TranscriptEntry represents a single entry in the assembled transcript.
type TranscriptEntry struct {
	Role      string       `json:"role"` // "user" or "assistant"
	Timestamp time.Time    `json:"timestamp"`
	Parts     []Part       `json:"parts"`
	MessageID string       `json:"messageID"`
	Tokens    *TokenUsage  `json:"tokens,omitempty"`
}

// TokenUsage contains token consumption info from a message.
type TokenUsage struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	CacheRead int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// Part represents a component of a message (text, tool call, etc.)
type Part struct {
	Type      string      `json:"type"` // "text", "tool", "step-start", "step-finish"
	ID        string      `json:"id"`
	Content   interface{} `json:"content,omitempty"`
	Timestamp time.Time   `json:"timestamp,omitempty"`
}

// TextPart represents a text content part.
type TextPart struct {
	Text string `json:"text"`
}

// ToolPart represents a tool call/result part.
type ToolPart struct {
	CallID string                 `json:"callID"`
	Tool   string                 `json:"tool"`
	Status string                 `json:"status"`
	Input  map[string]interface{} `json:"input"`
	Output string                 `json:"output"`
	Title  string                 `json:"title,omitempty"`
	Diff   string                 `json:"diff,omitempty"`
}

// Assembler reconstructs OpenCode transcripts from the fragmented storage format.
type Assembler struct {
	storageDir string
	logger     *logrus.Entry
}

// NewAssembler creates a new transcript assembler for the default OpenCode storage location.
func NewAssembler() (*Assembler, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	storageDir := filepath.Join(homeDir, ".local", "share", "opencode", "storage")
	if _, err := os.Stat(storageDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("OpenCode storage directory does not exist: %s", storageDir)
	}

	return &Assembler{
		storageDir: storageDir,
		logger:     logging.NewLogger("opencode-assembler"),
	}, nil
}

// AssembleTranscript reconstructs the full transcript for a given session ID.
func (a *Assembler) AssembleTranscript(sessionID string) ([]TranscriptEntry, error) {
	messagesDir := filepath.Join(a.storageDir, "message", sessionID)
	partsDir := filepath.Join(a.storageDir, "part")

	// Check if session message directory exists
	if _, err := os.Stat(messagesDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session message directory not found: %s", messagesDir)
	}

	// Read all message files
	messageFiles, err := os.ReadDir(messagesDir)
	if err != nil {
		return nil, fmt.Errorf("reading message directory: %w", err)
	}

	var entries []TranscriptEntry

	for _, msgFile := range messageFiles {
		if !strings.HasPrefix(msgFile.Name(), "msg_") || !strings.HasSuffix(msgFile.Name(), ".json") {
			continue
		}

		msgPath := filepath.Join(messagesDir, msgFile.Name())
		msgData, err := os.ReadFile(msgPath)
		if err != nil {
			a.logger.WithError(err).WithField("file", msgPath).Debug("Failed to read message file")
			continue
		}

		var msg struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Role      string `json:"role"`
			Time      struct {
				Created   int64 `json:"created"`
				Completed int64 `json:"completed"`
			} `json:"time"`
			Tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(msgData, &msg); err != nil {
			a.logger.WithError(err).WithField("file", msgPath).Debug("Failed to parse message")
			continue
		}

		// Load parts for this message
		msgPartsDir := filepath.Join(partsDir, msg.ID)
		var parts []Part

		if _, err := os.Stat(msgPartsDir); err == nil {
			partFiles, err := os.ReadDir(msgPartsDir)
			if err == nil {
				for _, partFile := range partFiles {
					if !strings.HasPrefix(partFile.Name(), "prt_") || !strings.HasSuffix(partFile.Name(), ".json") {
						continue
					}

					partPath := filepath.Join(msgPartsDir, partFile.Name())
					partData, err := os.ReadFile(partPath)
					if err != nil {
						continue
					}

					part, err := a.parsePart(partData)
					if err != nil {
						a.logger.WithError(err).WithField("file", partPath).Debug("Failed to parse part")
						continue
					}
					parts = append(parts, part)
				}
			}
		}

		// Sort parts by their ID (which contains timestamp info)
		sort.Slice(parts, func(i, j int) bool {
			return parts[i].ID < parts[j].ID
		})

		entry := TranscriptEntry{
			Role:      msg.Role,
			Timestamp: time.Unix(0, msg.Time.Created*int64(time.Millisecond)),
			Parts:     parts,
			MessageID: msg.ID,
		}

		// Add token usage if available
		if msg.Tokens.Input > 0 || msg.Tokens.Output > 0 {
			entry.Tokens = &TokenUsage{
				Input:     msg.Tokens.Input,
				Output:    msg.Tokens.Output,
				Reasoning: msg.Tokens.Reasoning,
				CacheRead: msg.Tokens.Cache.Read,
				CacheWrite: msg.Tokens.Cache.Write,
			}
		}

		entries = append(entries, entry)
	}

	// Sort entries by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries, nil
}

// parsePart parses a part JSON into a Part struct.
func (a *Assembler) parsePart(data []byte) (Part, error) {
	var basePart struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		MessageID string `json:"messageID"`
	}
	if err := json.Unmarshal(data, &basePart); err != nil {
		return Part{}, err
	}

	part := Part{
		ID:   basePart.ID,
		Type: basePart.Type,
	}

	switch basePart.Type {
	case "text":
		var textPart struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &textPart); err == nil {
			part.Content = TextPart{Text: textPart.Text}
		}

	case "tool":
		var toolPart struct {
			CallID string `json:"callID"`
			Tool   string `json:"tool"`
			State  struct {
				Status   string                 `json:"status"`
				Input    map[string]interface{} `json:"input"`
				Output   string                 `json:"output"`
				Title    string                 `json:"title"`
				Metadata struct {
					Diff string `json:"diff"`
				} `json:"metadata"`
			} `json:"state"`
		}
		if err := json.Unmarshal(data, &toolPart); err == nil {
			part.Content = ToolPart{
				CallID: toolPart.CallID,
				Tool:   toolPart.Tool,
				Status: toolPart.State.Status,
				Input:  toolPart.State.Input,
				Output: toolPart.State.Output,
				Title:  toolPart.State.Title,
				Diff:   toolPart.State.Metadata.Diff,
			}
		}

	case "step-start":
		var stepPart struct {
			Snapshot string `json:"snapshot"`
		}
		if err := json.Unmarshal(data, &stepPart); err == nil {
			part.Content = map[string]string{"snapshot": stepPart.Snapshot}
		}

	case "step-finish":
		var stepPart struct {
			Reason   string `json:"reason"`
			Snapshot string `json:"snapshot"`
			Cost     int    `json:"cost"`
			Tokens   struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(data, &stepPart); err == nil {
			part.Content = map[string]interface{}{
				"reason":   stepPart.Reason,
				"snapshot": stepPart.Snapshot,
				"tokens":   stepPart.Tokens,
			}
		}

	case "patch":
		var patchPart struct {
			Hash  string   `json:"hash"`
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(data, &patchPart); err == nil {
			part.Content = map[string]interface{}{
				"hash":  patchPart.Hash,
				"files": patchPart.Files,
			}
		}
	}

	return part, nil
}

// GetSessionMessages returns just the text messages without full part details.
// This is useful for a summary view.
func (a *Assembler) GetSessionMessages(sessionID string) ([]string, error) {
	entries, err := a.AssembleTranscript(sessionID)
	if err != nil {
		return nil, err
	}

	var messages []string
	for _, entry := range entries {
		for _, part := range entry.Parts {
			if part.Type == "text" {
				if textPart, ok := part.Content.(TextPart); ok {
					prefix := "User: "
					if entry.Role == "assistant" {
						prefix = "Assistant: "
					}
					messages = append(messages, prefix+textPart.Text)
				}
			}
		}
	}

	return messages, nil
}
