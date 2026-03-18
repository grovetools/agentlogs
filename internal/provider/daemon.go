package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/internal/transcript"
	"github.com/grovetools/core/pkg/daemon"
)

// DaemonSource reads and streams logs via the daemon's API.
// Normalization happens client-side using the appropriate provider normalizer.
type DaemonSource struct {
	client daemon.Client
	info   *session.SessionInfo
}

func NewDaemonSource(client daemon.Client, info *session.SessionInfo) *DaemonSource {
	return &DaemonSource{client: client, info: info}
}

func (s *DaemonSource) getNormalizer() transcript.Normalizer {
	switch s.info.Provider {
	case "codex":
		return transcript.NewCodexNormalizer()
	default:
		return transcript.NewClaudeNormalizer()
	}
}

func (s *DaemonSource) Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error) {
	logs, err := s.client.GetJobLogs(ctx, info.SessionID)
	if err != nil {
		return nil, fmt.Errorf("fetching daemon logs: %w", err)
	}

	normalizer := s.getNormalizer()
	var entries []transcript.UnifiedEntry

	for _, logLine := range logs {
		if entry, err := normalizer.NormalizeLine([]byte(logLine.Line)); err == nil && entry != nil {
			entries = append(entries, *entry)
		}
	}

	// Flush if the normalizer supports it (ClaudeNormalizer buffers tool calls)
	if flusher, ok := normalizer.(interface{ Flush() []*transcript.UnifiedEntry }); ok {
		for _, entry := range flusher.Flush() {
			entries = append(entries, *entry)
		}
	}

	return entries, nil
}

func (s *DaemonSource) Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error) {
	stream, err := s.client.StreamJobLogs(ctx, info.SessionID)
	if err != nil {
		return nil, fmt.Errorf("subscribing to daemon log stream: %w", err)
	}

	ch := make(chan transcript.UnifiedEntry, 100)
	normalizer := s.getNormalizer()

	go func() {
		defer close(ch)

		for event := range stream {
			if event.Event == "log" && event.Line != nil {
				lineBytes := []byte(event.Line.Line)
				entry, normErr := normalizer.NormalizeLine(lineBytes)

				if normErr != nil {
					// Not valid JSON - raw text log (e.g. bash job output).
					// Emit as a plain text entry so callers can display it.
					textEntry := transcript.UnifiedEntry{
						Role:     "assistant",
						Provider: info.Provider,
						Parts: []transcript.UnifiedPart{{
							Type:    "text",
							Content: transcript.UnifiedTextContent{Text: event.Line.Line},
						}},
					}
					select {
					case ch <- textEntry:
					case <-ctx.Done():
						return
					}
				} else if entry != nil {
					select {
					case ch <- *entry:
					case <-ctx.Done():
						return
					}
				}
			} else if event.Event == "status" {
				if event.Status == "completed" || event.Status == "failed" || event.Status == "cancelled" {
					if event.Error != "" {
						fmt.Fprintf(os.Stderr, "Error: %s\n", event.Error)
					}
					return
				}
			}
		}

		// Flush remaining buffered entries
		if flusher, ok := normalizer.(interface{ Flush() []*transcript.UnifiedEntry }); ok {
			for _, entry := range flusher.Flush() {
				select {
				case ch <- *entry:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}
