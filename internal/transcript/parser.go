package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TranscriptEntry represents a single entry in the Claude JSONL transcript
type TranscriptEntry struct {
	SessionID  string    `json:"sessionId"`
	Message    *Message  `json:"message,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	Type       string    `json:"type"`
	UserType   string    `json:"userType"`
	UUID       string    `json:"uuid"`
	ParentUUID string    `json:"parentUuid"`
}

// Message represents a Claude message
type Message struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Model        string          `json:"model"`
	Content      json.RawMessage `json:"content"` // Can be string or []Content
	StopReason   *string         `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence"`
	Usage        *Usage          `json:"usage"`
}

// Content represents message content
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage represents token usage information
type Usage struct {
	InputTokens              int    `json:"input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	ServiceTier              string `json:"service_tier"`
}

// ExtractedMessage represents a simplified message for storage
type ExtractedMessage struct {
	SessionID  string
	MessageID  string
	Timestamp  time.Time
	Role       string
	Content    string
	RawContent json.RawMessage
	Metadata   map[string]any
}

// Parser handles JSONL transcript parsing
type Parser struct {
	// Nothing needed for now, but allows for future configuration
}

// NewParser creates a new transcript parser
func NewParser() *Parser {
	return &Parser{}
}

// ParseFile parses an entire JSONL file and extracts messages
func (p *Parser) ParseFile(path string) ([]ExtractedMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return p.parseFromReader(file, 0)
}

// ParseFileFromOffset parses a JSONL file starting from a specific byte offset
func (p *Parser) ParseFileFromOffset(path string, offset int64) ([]ExtractedMessage, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Seek to the offset
	if offset > 0 {
		if _, err := file.Seek(offset, 0); err != nil {
			return nil, offset, fmt.Errorf("failed to seek to offset %d: %w", offset, err)
		}
	}

	messages, err := p.parseFromReader(file, offset)
	if err != nil {
		return nil, offset, err
	}

	// Get new offset
	newOffset, err := file.Seek(0, 1) // Get current position
	if err != nil {
		return messages, offset, fmt.Errorf("failed to get new offset: %w", err)
	}

	return messages, newOffset, nil
}

// parseFromReader parses JSONL from a reader
func (p *Parser) parseFromReader(file *os.File, startOffset int64) ([]ExtractedMessage, error) {
	var messages []ExtractedMessage
	scanner := bufio.NewScanner(file)

	// Increase buffer size for large JSON lines
	const maxScanTokenSize = 1024 * 1024 // 1MB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScanTokenSize)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		var entry TranscriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Log but don't fail on individual line errors
			fmt.Printf("Warning: Failed to parse line %d: %v\n", lineNum, err)
			continue
		}

		// Extract messages from entries with message type
		if entry.Type == "assistant" && entry.Message != nil && entry.Message.Type == "message" {
			extracted := p.extractMessage(entry)
			if extracted != nil {
				messages = append(messages, *extracted)
			}
		} else if entry.Type == "user" && entry.Message != nil {
			// Also extract user messages
			extracted := p.extractMessage(entry)
			if extracted != nil {
				messages = append(messages, *extracted)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return messages, fmt.Errorf("scanner error: %w", err)
	}

	return messages, nil
}

// extractMessage extracts a simplified message from a transcript entry
func (p *Parser) extractMessage(entry TranscriptEntry) *ExtractedMessage {
	if entry.Message == nil {
		return nil
	}

	// Handle both string and array content formats
	var textContent string

	// First try to unmarshal as string (user messages)
	var stringContent string
	if err := json.Unmarshal(entry.Message.Content, &stringContent); err == nil {
		textContent = stringContent
	} else {
		// Try to unmarshal as array of Content (assistant messages)
		var contentArray []Content
		if err := json.Unmarshal(entry.Message.Content, &contentArray); err == nil {
			// Combine all text content
			for _, content := range contentArray {
				if content.Type == "text" {
					if textContent != "" {
						textContent += "\n"
					}
					textContent += content.Text
				}
			}
		}
	}

	// Skip if no text content
	if textContent == "" {
		return nil
	}

	// Determine role based on entry type if not set
	role := entry.Message.Role
	if role == "" {
		role = entry.Type // "user" or "assistant"
	}

	// Generate message ID if not present (for user messages)
	messageID := entry.Message.ID
	if messageID == "" {
		messageID = fmt.Sprintf("%s_%s", entry.Type, entry.UUID)
	}

	// Prepare metadata
	metadata := make(map[string]any)
	if entry.Message.Model != "" {
		metadata["model"] = entry.Message.Model
	}
	if entry.Message.Usage != nil {
		metadata["usage"] = entry.Message.Usage
	}
	if entry.Message.StopReason != nil {
		metadata["stop_reason"] = *entry.Message.StopReason
	}
	metadata["uuid"] = entry.UUID
	metadata["parent_uuid"] = entry.ParentUUID
	metadata["user_type"] = entry.UserType

	return &ExtractedMessage{
		SessionID:  entry.SessionID,
		MessageID:  messageID,
		Timestamp:  entry.Timestamp,
		Role:       role,
		Content:    textContent,
		RawContent: entry.Message.Content, // Keep the raw JSON
		Metadata:   metadata,
	}
}

// GetTranscriptPath finds the transcript path for a session
func GetTranscriptPath(sessionID string) (string, error) {
	// Claude stores transcripts in a predictable location
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Look for the transcript file
	pattern := fmt.Sprintf("%s/.claude/projects/*/%s.jsonl", homeDir, sessionID)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("transcript not found for session %s", sessionID)
	}

	return matches[0], nil
}
