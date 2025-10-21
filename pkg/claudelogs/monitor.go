package claudelogs

import (
	"database/sql"
	"time"

	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
)

// Monitor wraps the internal transcript monitor
type Monitor struct {
	*transcript.Monitor
}

// NewMonitor creates a new transcript monitor
func NewMonitor(db *sql.DB, checkInterval time.Duration) *Monitor {
	return &Monitor{
		Monitor: transcript.NewMonitor(db, checkInterval),
	}
}

// NewMonitorWithConfig creates a new transcript monitor with custom configuration
func NewMonitorWithConfig(db *sql.DB, checkInterval time.Duration, summaryConfig SummaryConfig) *Monitor {
	internalConfig := transcript.SummaryConfig{
		Enabled:          summaryConfig.Enabled,
		LLMCommand:       summaryConfig.LLMCommand,
		UpdateInterval:   summaryConfig.UpdateInterval,
		CurrentWindow:    summaryConfig.CurrentWindow,
		RecentWindow:     summaryConfig.RecentWindow,
		MaxInputTokens:   summaryConfig.MaxInputTokens,
		MilestoneEnabled: summaryConfig.MilestoneEnabled,
	}
	
	return &Monitor{
		Monitor: transcript.NewMonitorWithConfig(db, checkInterval, internalConfig),
	}
}

// SummaryConfig for monitor configuration
type SummaryConfig struct {
	Enabled          bool
	LLMCommand       string
	UpdateInterval   int
	CurrentWindow    int
	RecentWindow     int
	MaxInputTokens   int
	MilestoneEnabled bool
}

// Start begins monitoring for new transcript entries
func (m *Monitor) Start() {
	m.Monitor.Start()
}

// Stop gracefully stops the monitor
func (m *Monitor) Stop() {
	m.Monitor.Stop()
}