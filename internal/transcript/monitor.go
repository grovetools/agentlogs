package transcript

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
)

// Monitor handles periodic transcript monitoring and extraction
type Monitor struct {
	db             *sql.DB
	parser         *Parser
	checkInterval  time.Duration
	fileOffsets    map[string]int64 // sessionID -> file offset
	offsetsMutex   sync.RWMutex
	stopChan       chan struct{}
	wg             sync.WaitGroup
	summaryManager *SummaryManager
}

// NewMonitor creates a new transcript monitor
func NewMonitor(db *sql.DB, checkInterval time.Duration) *Monitor {
	return &Monitor{
		db:             db,
		parser:         NewParser(),
		checkInterval:  checkInterval,
		fileOffsets:    make(map[string]int64),
		stopChan:       make(chan struct{}),
		summaryManager: NewSummaryManager(db),
	}
}

// NewMonitorWithConfig creates a new transcript monitor with provided summary config
func NewMonitorWithConfig(db *sql.DB, checkInterval time.Duration, summaryConfig SummaryConfig) *Monitor {
	return &Monitor{
		db:             db,
		parser:         NewParser(),
		checkInterval:  checkInterval,
		fileOffsets:    make(map[string]int64),
		stopChan:       make(chan struct{}),
		summaryManager: NewSummaryManagerWithConfig(db, summaryConfig),
	}
}

// Start begins the monitoring process
func (m *Monitor) Start() {
	log.Println("Starting transcript monitor...")

	// Load existing offsets from database
	m.loadOffsets()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		// Initial check immediately
		m.processActiveSessions()

		ticker := time.NewTicker(m.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.processActiveSessions()
			case <-m.stopChan:
				log.Println("Stopping transcript monitor...")
				return
			}
		}
	}()
}

// Stop gracefully stops the monitor
func (m *Monitor) Stop() {
	close(m.stopChan)
	m.wg.Wait()
}

// loadOffsets loads extraction state from the database
func (m *Monitor) loadOffsets() {
	rows, err := m.db.Query(`
		SELECT id, session_summary 
		FROM sessions 
		WHERE is_deleted = FALSE AND status = 'running'
	`)
	if err != nil {
		log.Printf("Failed to load offsets: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		var summaryJSON sql.NullString

		if err := rows.Scan(&sessionID, &summaryJSON); err != nil {
			log.Printf("Failed to scan session: %v", err)
			continue
		}

		if summaryJSON.Valid {
			var summary map[string]any
			if err := json.Unmarshal([]byte(summaryJSON.String), &summary); err == nil {
				// Extract offset from extraction_state
				if extractionState, ok := summary["extraction_state"].(map[string]any); ok {
					if offset, ok := extractionState["file_offset"].(float64); ok {
						m.offsetsMutex.Lock()
						m.fileOffsets[sessionID] = int64(offset)
						m.offsetsMutex.Unlock()
					}
				}
			}
		}
	}
}

// processActiveSessions checks all active sessions for new messages
func (m *Monitor) processActiveSessions() {
	// Get active sessions
	sessions, err := m.getActiveSessions()
	if err != nil {
		log.Printf("Failed to get active sessions: %v", err)
		return
	}

	log.Printf("Processing %d active sessions", len(sessions))
	for _, session := range sessions {
		m.processSession(session)
	}
}

// getActiveSessions retrieves all active sessions from the database
func (m *Monitor) getActiveSessions() ([]*models.Session, error) {
	// Query active and recently completed sessions
	rows, err := m.db.Query(`
		SELECT id, pid, repo, branch, tmux_key, working_directory, user,
		       status, started_at, ended_at, last_activity, is_test,
		       tool_stats, session_summary
		FROM sessions
		WHERE is_deleted = FALSE 
		  AND (status = 'running' 
		       OR (status = 'completed' AND ended_at > datetime('now', '-5 minutes')))
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		session := &models.Session{}
		var toolStatsJSON, sessionSummaryJSON sql.NullString
		var endedAt sql.NullTime

		err := rows.Scan(
			&session.ID, &session.PID, &session.Repo, &session.Branch,
			&session.TmuxKey, &session.WorkingDirectory, &session.User,
			&session.Status, &session.StartedAt, &endedAt, &session.LastActivity,
			&session.IsTest, &toolStatsJSON, &sessionSummaryJSON,
		)
		if err != nil {
			continue
		}

		if endedAt.Valid {
			session.EndedAt = &endedAt.Time
		}

		// Parse JSON fields
		if toolStatsJSON.Valid {
			json.Unmarshal([]byte(toolStatsJSON.String), &session.ToolStats)
		}
		if sessionSummaryJSON.Valid {
			var summary models.Summary
			if err := json.Unmarshal([]byte(sessionSummaryJSON.String), &summary); err == nil {
				session.SessionSummary = &summary
			}
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// processSession processes a single session for new messages
func (m *Monitor) processSession(session *models.Session) {
	log.Printf("Processing session %s (status: %s)", session.ID, session.Status)

	// Find transcript file
	transcriptPath, err := GetTranscriptPath(session.ID)
	if err != nil {
		// This is normal if Claude hasn't created the file yet
		log.Printf("Transcript not found for session %s: %v", session.ID, err)
		return
	}
	log.Printf("Found transcript for session %s at %s", session.ID, transcriptPath)

	// Get current offset
	m.offsetsMutex.RLock()
	offset := m.fileOffsets[session.ID]
	m.offsetsMutex.RUnlock()

	// Parse new messages from offset
	messages, newOffset, err := m.parser.ParseFileFromOffset(transcriptPath, offset)
	if err != nil {
		log.Printf("Failed to parse transcript for session %s: %v", session.ID, err)
		return
	}

	// If no new messages, nothing to do
	if len(messages) == 0 {
		return
	}

	log.Printf("Found %d new messages for session %s", len(messages), session.ID)

	// Store messages in database
	if err := m.storeMessages(messages); err != nil {
		log.Printf("Failed to store messages for session %s: %v", session.ID, err)
		return
	} else {
		log.Printf("Successfully stored %d messages for session %s", len(messages), session.ID)
	}

	// Update offset
	m.offsetsMutex.Lock()
	m.fileOffsets[session.ID] = newOffset
	m.offsetsMutex.Unlock()

	// Update extraction state in database
	if err := m.updateExtractionState(session.ID, transcriptPath, newOffset, messages[len(messages)-1].MessageID); err != nil {
		log.Printf("Failed to update extraction state for session %s: %v", session.ID, err)
	}

	// Check if we should update summaries
	totalMessages, err := m.getMessageCount(session.ID)
	if err != nil {
		log.Printf("Failed to get message count for session %s: %v", session.ID, err)
	} else {
		log.Printf("Total messages for session %s: %d", session.ID, totalMessages)
		if m.summaryManager.ShouldUpdateSummary(session.ID, totalMessages) {
			log.Printf("Updating summary for session %s (message count: %d)", session.ID, totalMessages)
			if err := m.summaryManager.UpdateSessionSummary(session.ID); err != nil {
				log.Printf("Failed to update summary for session %s: %v", session.ID, err)
			} else {
				log.Printf("Successfully updated summary for session %s", session.ID)
			}
		}
	}
}

// storeMessages stores extracted messages in the database
func (m *Monitor) storeMessages(messages []ExtractedMessage) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO claude_messages 
		(id, session_id, message_id, timestamp, role, content, raw_content, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, msg := range messages {
		// Generate ID (session_id + message_id)
		id := fmt.Sprintf("%s_%s", msg.SessionID, msg.MessageID)

		metadataJSON, err := json.Marshal(msg.Metadata)
		if err != nil {
			return err
		}

		result, err := stmt.Exec(
			id,
			msg.SessionID,
			msg.MessageID,
			msg.Timestamp,
			msg.Role,
			msg.Content,
			msg.RawContent,
			metadataJSON,
		)
		if err != nil {
			log.Printf("Failed to insert message %s: %v", id, err)
			return err
		}

		// Check if insert was successful
		affected, _ := result.RowsAffected()
		if affected == 0 {
			log.Printf("WARNING: No rows affected when inserting message %s", id)
		}
	}

	return tx.Commit()
}

// updateExtractionState updates the extraction state in the session summary
func (m *Monitor) updateExtractionState(sessionID, transcriptPath string, offset int64, lastMessageID string) error {
	// Get current session summary
	var summaryJSON sql.NullString
	err := m.db.QueryRow(`
		SELECT session_summary FROM sessions WHERE id = ?
	`, sessionID).Scan(&summaryJSON)
	if err != nil {
		return err
	}

	// Parse or create summary
	var summary map[string]any
	if summaryJSON.Valid && summaryJSON.String != "" {
		if err := json.Unmarshal([]byte(summaryJSON.String), &summary); err != nil {
			log.Printf("Failed to parse session_summary for %s: %v", sessionID, err)
			// If parsing fails, start fresh
			summary = make(map[string]any)
		}
	} else {
		// Initialize if NULL
		summary = make(map[string]any)
	}

	// Ensure summary is not nil
	if summary == nil {
		log.Printf("WARNING: summary is nil for session %s, creating new map", sessionID)
		summary = make(map[string]any)
	}

	// Update extraction state
	summary["extraction_state"] = map[string]any{
		"transcript_path": transcriptPath,
		"file_offset":     offset,
		"last_message_id": lastMessageID,
		"last_extraction": time.Now().Format(time.RFC3339),
	}

	// Update message stats
	var totalMessages, userMessages, assistantMessages int
	err = m.db.QueryRow(`
		SELECT 
			COUNT(*) as total,
			SUM(CASE WHEN role = 'user' THEN 1 ELSE 0 END) as user_count,
			SUM(CASE WHEN role = 'assistant' THEN 1 ELSE 0 END) as assistant_count
		FROM claude_messages 
		WHERE session_id = ?
	`, sessionID).Scan(&totalMessages, &userMessages, &assistantMessages)

	if err == nil {
		summary["message_stats"] = map[string]any{
			"total_messages":     totalMessages,
			"user_messages":      userMessages,
			"assistant_messages": assistantMessages,
			"last_extraction":    time.Now().Format(time.RFC3339),
		}
	}

	// Marshal and update
	newSummaryJSON, err := json.Marshal(summary)
	if err != nil {
		return err
	}

	_, err = m.db.Exec(`
		UPDATE sessions 
		SET session_summary = ?, last_activity = CURRENT_TIMESTAMP
		WHERE id = ?
	`, string(newSummaryJSON), sessionID)

	return err
}

// getMessageCount returns the total message count for a session
func (m *Monitor) getMessageCount(sessionID string) (int, error) {
	var count int
	err := m.db.QueryRow(`
		SELECT COUNT(*) FROM claude_messages WHERE session_id = ?
	`, sessionID).Scan(&count)
	return count, err
}
