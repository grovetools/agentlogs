package transcript

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/grovetools/core/pkg/models"
	"gopkg.in/yaml.v3"
)

// SummaryManager handles AI summary generation for sessions
type SummaryManager struct {
	db               *sql.DB
	config           SummaryConfig
	lastSummaryAt    map[string]int // sessionID -> message count at last summary
	lastSummaryMutex sync.RWMutex
}

// SummaryConfig holds configuration for summary generation
type SummaryConfig struct {
	Enabled          bool   `yaml:"enabled"`
	LLMCommand       string `yaml:"llm_command"`
	UpdateInterval   int    `yaml:"update_interval"` // Update every N messages
	CurrentWindow    int    `yaml:"current_window"`  // Messages for current activity
	RecentWindow     int    `yaml:"recent_window"`   // Messages for recent context
	MaxInputTokens   int    `yaml:"max_input_tokens"`
	MilestoneEnabled bool   `yaml:"milestone_detection"`
}

// SessionSummary represents the AI-generated summary
type SessionSummary struct {
	CurrentActivity string            `json:"current_activity"`
	History         []models.Milestone `json:"history"` // Renamed from Milestones, stores append-only history
	LastUpdated     time.Time         `json:"last_updated"`
	UpdateCount     int               `json:"update_count"`
	NextUpdateAt    int               `json:"next_update_at_message"`
}

// Common prompt instructions for all summary types
const summaryPromptInstructions = `
**NEVER** say anything along the lines of:

* "here is a summary"
* "based on the messages"
* "the recent work"
* "the user requested".

Do not state anything about the LLM producing the summary or doing the coding work.

We only want direct info related to the programming tasks being completed.`

// NewSummaryManager creates a new summary manager
func NewSummaryManager(db *sql.DB) *SummaryManager {
	return &SummaryManager{
		db:            db,
		config:        loadSummaryConfig(),
		lastSummaryAt: make(map[string]int),
	}
}

// NewSummaryManagerWithConfig creates a new summary manager with provided config
func NewSummaryManagerWithConfig(db *sql.DB, config SummaryConfig) *SummaryManager {
	return &SummaryManager{
		db:            db,
		config:        config,
		lastSummaryAt: make(map[string]int),
	}
}

// loadSummaryConfig loads configuration from the config file
func loadSummaryConfig() SummaryConfig {
	defaultConfig := SummaryConfig{
		Enabled:          false,
		LLMCommand:       "llm -m gpt-4o-mini",
		UpdateInterval:   10,
		CurrentWindow:    10,
		RecentWindow:     30,
		MaxInputTokens:   8000,
		MilestoneEnabled: true,
	}

	// Try to load from config file
	configPath := expandPath("~/.config/tmux-claude-hud/config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}

	var config struct {
		ConversationSummarization SummaryConfig `yaml:"conversation_summarization"`
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return defaultConfig
	}

	if config.ConversationSummarization.Enabled {
		return config.ConversationSummarization
	}

	return defaultConfig
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

// ShouldUpdateSummary checks if a summary update is due
func (sm *SummaryManager) ShouldUpdateSummary(sessionID string, currentMessageCount int) bool {
	if !sm.config.Enabled {
		return false
	}

	sm.lastSummaryMutex.RLock()
	lastCount := sm.lastSummaryAt[sessionID]
	sm.lastSummaryMutex.RUnlock()

	return currentMessageCount-lastCount >= sm.config.UpdateInterval
}

// UpdateSessionSummary generates and updates the summary for a session
func (sm *SummaryManager) UpdateSessionSummary(sessionID string) error {
	if !sm.config.Enabled {
		return nil
	}

	// Get all messages for the session
	messages, err := sm.getSessionMessages(sessionID)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	if len(messages) == 0 {
		return nil
	}

	// Generate progressive summary
	summary, err := sm.generateProgressiveSummary(sessionID, messages)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// Update database
	if err := sm.storeSummary(sessionID, summary); err != nil {
		return fmt.Errorf("failed to store summary: %w", err)
	}

	// Update last summary count
	sm.lastSummaryMutex.Lock()
	sm.lastSummaryAt[sessionID] = len(messages)
	sm.lastSummaryMutex.Unlock()

	return nil
}

// getSessionMessages retrieves all messages for a session
func (sm *SummaryManager) getSessionMessages(sessionID string) ([]ExtractedMessage, error) {
	rows, err := sm.db.Query(`
		SELECT message_id, timestamp, role, content, raw_content, metadata
		FROM claude_messages
		WHERE session_id = ?
		ORDER BY timestamp ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ExtractedMessage
	for rows.Next() {
		var msg ExtractedMessage
		var rawContent []byte
		var metadataJSON []byte

		err := rows.Scan(&msg.MessageID, &msg.Timestamp, &msg.Role, &msg.Content, &rawContent, &metadataJSON)
		if err != nil {
			return nil, err
		}

		msg.SessionID = sessionID
		msg.RawContent = rawContent

		if len(metadataJSON) > 0 {
			json.Unmarshal(metadataJSON, &msg.Metadata)
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

// generateProgressiveSummary creates a multi-level summary
func (sm *SummaryManager) generateProgressiveSummary(sessionID string, messages []ExtractedMessage) (*SessionSummary, error) {
	// Get existing summary to preserve history and track update count
	existingSummary, _ := sm.getExistingSummary(sessionID)
	
	var updateCount int
	var history []models.Milestone
	
	if existingSummary != nil {
		updateCount = existingSummary.UpdateCount
		history = existingSummary.History
	}
	updateCount++

	summary := &SessionSummary{
		LastUpdated:  time.Now(),
		UpdateCount:  updateCount,
		NextUpdateAt: len(messages) + sm.config.UpdateInterval,
		History:      history,
	}

	// Generate current activity summary (last N messages)
	if len(messages) > 0 {
		start := max(0, len(messages)-sm.config.CurrentWindow)
		currentMessages := messages[start:]

		currentActivity, err := sm.generateCurrentActivitySummary(currentMessages)
		if err != nil {
			log.Printf("Failed to generate current activity summary: %v", err)
		} else {
			summary.CurrentActivity = currentActivity
			
			// Add current activity to history as a new entry
			historyEntry := models.Milestone{
				Timestamp: time.Now(),
				Summary:   currentActivity,
			}
			summary.History = append(summary.History, historyEntry)
		}
	}

	return summary, nil
}

// generateCurrentActivitySummary creates a summary of the most recent activity
func (sm *SummaryManager) generateCurrentActivitySummary(messages []ExtractedMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	// Prepare conversation for LLM
	conversation := sm.formatMessagesForLLM(messages)

	prompt := fmt.Sprintf(`Based on the last few messages, what is Claude's immediate task?

**CRITICAL INSTRUCTIONS:**
1. Respond with a single, concise sentence.
2. DO NOT use bullet points or lists.
3. The sentence MUST start with "• ".
4. Use <strong> tags to highlight 1-2 key technical terms or actions.
5. DO NOT mention "the user" or "Claude". Focus only on the task.

Example: • Refactoring the <strong>authentication middleware</strong> to support <strong>OAuth2</strong>.

Recent conversation:
%s

Current activity summary:`, conversation)

	return sm.callLLM(prompt)
}



// formatMessagesForLLM formats messages for LLM consumption
func (sm *SummaryManager) formatMessagesForLLM(messages []ExtractedMessage) string {
	var buffer strings.Builder

	// Estimate tokens and truncate if needed
	totalChars := 0
	maxChars := sm.config.MaxInputTokens * 3 // Rough estimate: 3 chars per token

	for i, msg := range messages {
		role := "User"
		if msg.Role == "assistant" {
			role = "Claude"
		}

		line := fmt.Sprintf("%s: %s\n\n", role, msg.Content)

		if totalChars+len(line) > maxChars {
			buffer.WriteString(fmt.Sprintf("[... %d earlier messages truncated ...]\n\n", i))
			break
		}

		buffer.WriteString(line)
		totalChars += len(line)
	}

	return buffer.String()
}

// callLLM executes the LLM command with the given prompt
func (sm *SummaryManager) callLLM(prompt string) (string, error) {
	cmdParts := strings.Fields(sm.config.LLMCommand)
	if len(cmdParts) == 0 {
		return "", fmt.Errorf("invalid LLM command")
	}

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.Stdin = strings.NewReader(prompt)

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("LLM command failed: %v, stderr: %s", err, errOut.String())
	}

	return strings.TrimSpace(out.String()), nil
}

// getExistingSummary retrieves the current summary from the database
func (sm *SummaryManager) getExistingSummary(sessionID string) (*SessionSummary, error) {
	var summaryJSON sql.NullString
	err := sm.db.QueryRow(`
		SELECT session_summary FROM sessions WHERE id = ?
	`, sessionID).Scan(&summaryJSON)

	if err != nil || !summaryJSON.Valid {
		return nil, err
	}

	var sessionData map[string]any
	if err := json.Unmarshal([]byte(summaryJSON.String), &sessionData); err != nil {
		return nil, err
	}

	// Extract AI summary if it exists
	aiSummaryData, ok := sessionData["ai_summary"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// Convert to SessionSummary struct
	summaryJSON2, err := json.Marshal(aiSummaryData)
	if err != nil {
		return nil, err
	}

	var summary SessionSummary
	if err := json.Unmarshal(summaryJSON2, &summary); err != nil {
		return nil, err
	}

	return &summary, nil
}

// storeSummary updates the session summary in the database
func (sm *SummaryManager) storeSummary(sessionID string, summary *SessionSummary) error {
	// Get current session summary
	var currentSummaryJSON sql.NullString
	err := sm.db.QueryRow(`
		SELECT session_summary FROM sessions WHERE id = ?
	`, sessionID).Scan(&currentSummaryJSON)
	if err != nil {
		return err
	}

	// Parse or create summary object
	sessionData := make(map[string]any)
	if currentSummaryJSON.Valid {
		if err := json.Unmarshal([]byte(currentSummaryJSON.String), &sessionData); err != nil {
			sessionData = make(map[string]any)
		}
	}

	// Update AI summary section
	sessionData["ai_summary"] = summary

	// Marshal and update
	newSummaryJSON, err := json.Marshal(sessionData)
	if err != nil {
		return err
	}

	_, err = sm.db.Exec(`
		UPDATE sessions 
		SET session_summary = ?
		WHERE id = ?
	`, string(newSummaryJSON), sessionID)

	return err
}

// Helper function for max
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
