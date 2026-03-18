package provider

import (
	"context"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/internal/transcript"
)

// ReadOptions controls transcript reading behavior.
type ReadOptions struct {
	DetailLevel  string // "summary" or "full"
	MaxDiffLines int    // 0 = unlimited
	StartLine    int    // Skip lines before this index (for job-scoped reads)
	EndLine      int    // Stop at this line index (-1 = read to end)
}

// TranscriptSource provides read and stream access to agent transcripts
// for a specific provider format. Normalization into UnifiedEntry happens
// here, not in the daemon - each provider knows its own log format.
type TranscriptSource interface {
	// Read returns the full normalized transcript.
	Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error)

	// Stream tails live output, emitting normalized entries.
	// The channel closes when the context is cancelled or the session ends.
	Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error)
}
