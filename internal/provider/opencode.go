package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/grovetools/agentlogs/internal/opencode"
	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// OpenCodeSource reads and streams OpenCode transcripts via the Assembler.
type OpenCodeSource struct{}

func NewOpenCodeSource() *OpenCodeSource {
	return &OpenCodeSource{}
}

func (s *OpenCodeSource) Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error) {
	assembler, err := opencode.NewAssembler()
	if err != nil {
		return nil, fmt.Errorf("creating OpenCode assembler: %w", err)
	}

	entries, err := assembler.AssembleTranscript(info.SessionID)
	if err != nil {
		return nil, fmt.Errorf("assembling OpenCode transcript: %w", err)
	}

	normalizer := transcript.NewOpenCodeNormalizer()
	return normalizer.NormalizeAll(entries), nil
}

func (s *OpenCodeSource) Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error) {
	assembler, err := opencode.NewAssembler()
	if err != nil {
		return nil, fmt.Errorf("creating OpenCode assembler: %w", err)
	}

	ch := make(chan transcript.UnifiedEntry, 100)
	normalizer := transcript.NewOpenCodeNormalizer()

	go func() {
		defer close(ch)

		seenMessages := make(map[string]bool)

		// Initial display of existing messages
		entries, err := assembler.AssembleTranscript(info.SessionID)
		if err == nil {
			for _, entry := range entries {
				seenMessages[entry.MessageID] = true
				if unified := normalizer.NormalizeEntry(entry); unified != nil {
					select {
					case ch <- *unified:
					case <-ctx.Done():
						return
					}
				}
			}
		}

		// Poll for new messages
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}

			entries, err := assembler.AssembleTranscript(info.SessionID)
			if err != nil {
				continue
			}

			for _, entry := range entries {
				if !seenMessages[entry.MessageID] {
					seenMessages[entry.MessageID] = true
					if unified := normalizer.NormalizeEntry(entry); unified != nil {
						select {
						case ch <- *unified:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()

	return ch, nil
}
