package agentstream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// StreamOptions configures transcript streaming.
type StreamOptions struct {
	TranscriptPath string              // Direct path (if already known)
	Discover       *DiscoverOptions    // Or discover automatically
	Normalizer     transcript.Normalizer // Optional override; defaults based on provider
}

// Flusher is an optional interface for normalizers that buffer entries.
type Flusher interface {
	Flush() []*transcript.UnifiedEntry
}

// Stream tails a transcript file and emits normalized entries on a channel.
// Blocks until ctx is cancelled. Handles EOF polling and normalizer flushing.
func Stream(ctx context.Context, opts StreamOptions) (<-chan transcript.UnifiedEntry, error) {
	transcriptPath := opts.TranscriptPath

	// Resolve path via discovery if not provided directly
	if transcriptPath == "" && opts.Discover != nil {
		var err error
		transcriptPath, err = waitForTranscript(ctx, *opts.Discover)
		if err != nil {
			return nil, err
		}
	}

	if transcriptPath == "" {
		return nil, fmt.Errorf("no transcript path provided and no discovery options set")
	}

	// Default normalizer based on provider
	normalizer := opts.Normalizer
	if normalizer == nil {
		if opts.Discover != nil {
			normalizer = normalizerForProvider(opts.Discover.Provider)
		} else {
			normalizer = transcript.NewClaudeNormalizer()
		}
	}

	ch := make(chan transcript.UnifiedEntry, 64)

	go func() {
		defer close(ch)
		tailFile(ctx, transcriptPath, normalizer, ch)
	}()

	return ch, nil
}

// waitForTranscript polls for a transcript file until it appears or ctx is cancelled.
func waitForTranscript(ctx context.Context, opts DiscoverOptions) (string, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return "", fmt.Errorf("timeout waiting for transcript file")
		case <-ticker.C:
			path, err := DiscoverTranscript(opts)
			if err == nil {
				return path, nil
			}
		}
	}
}

// tailFile reads a JSONL file, tailing it for new content.
func tailFile(ctx context.Context, path string, normalizer transcript.Normalizer, ch chan<- transcript.UnifiedEntry) {
	for {
		// Wait for file to exist
		if _, err := os.Stat(path); os.IsNotExist(err) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}
		break
	}

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	for {
		select {
		case <-ctx.Done():
			// Flush any remaining buffered entries
			if flusher, ok := normalizer.(Flusher); ok {
				for _, entry := range flusher.Flush() {
					select {
					case ch <- *entry:
					default:
					}
				}
			}
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// Flush on EOF (tool calls may be buffered waiting for results)
				if flusher, ok := normalizer.(Flusher); ok {
					for _, entry := range flusher.Flush() {
						select {
						case ch <- *entry:
						case <-ctx.Done():
							return
						}
					}
				}

				// Wait for more data
				select {
				case <-ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
					continue
				}
			}
			return
		}

		if len(line) <= 1 {
			continue
		}

		entry, err := normalizer.NormalizeLine(line)
		if err != nil || entry == nil {
			continue
		}

		select {
		case ch <- *entry:
		case <-ctx.Done():
			return
		}
	}
}

// normalizerForProvider returns the appropriate normalizer for a given provider.
func normalizerForProvider(provider string) transcript.Normalizer {
	switch provider {
	case "codex":
		return transcript.NewCodexNormalizer()
	case "opencode":
		return transcript.NewOpenCodeNormalizer()
	default:
		return transcript.NewClaudeNormalizer()
	}
}
