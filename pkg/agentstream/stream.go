package agentstream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// StreamOptions configures transcript streaming.
type StreamOptions struct {
	TranscriptPath string                // Direct path (if already known)
	Discover       *DiscoverOptions      // Or discover automatically
	Normalizer     transcript.Normalizer // Optional override; defaults based on provider
	// Provider selects the normalizer when TranscriptPath is given directly
	// (with Discover, its Provider already routes). Empty defaults to claude,
	// the historical behavior.
	Provider string
	// StartOffset resumes reading at this byte offset instead of the start of
	// the file. It must be a value previously reported by a checkpoint Event
	// (which only ever points at a fully-consumed line boundary). If the file
	// no longer supports the offset — it shrank, or the byte before the offset
	// is not a newline because the file was rewritten — the stream falls back
	// to a full read from byte 0 and emits a Reset event first so the consumer
	// can drop state it accumulated from the previous stream. The zero value
	// reads from the start of the file, the historical behavior.
	StartOffset int64
}

// Event is the envelope emitted by StreamEvents. Exactly one of three shapes:
//   - entry event: Entry != nil, a normalized transcript entry.
//   - checkpoint event: Entry == nil, Reset false. Offset is the byte offset
//     of the last fully-consumed line boundary, emitted once the normalizer
//     has flushed, so every entry derived from bytes before Offset has
//     already been sent. Offset is therefore safe to persist and pass as
//     StartOffset on a later stream without losing entries; entries emitted
//     AFTER the latest checkpoint may be re-emitted by such a resume.
//   - reset event: Reset true. StartOffset could not be honored (the file was
//     truncated or rewritten); the stream restarts from byte 0 and the
//     consumer must discard anything it cached from earlier streams.
//
// Offset is meaningful only on checkpoint events.
type Event struct {
	Entry  *transcript.UnifiedEntry
	Offset int64
	Reset  bool
}

// Flusher is an optional interface for normalizers that buffer entries.
type Flusher interface {
	Flush() []*transcript.UnifiedEntry
}

// Stream tails a transcript file and emits normalized entries on a channel.
// Blocks until ctx is cancelled. Handles EOF polling and normalizer flushing.
// It is StreamEvents with the envelope stripped: checkpoint and reset events
// are dropped, so consumers that need resume support use StreamEvents.
func Stream(ctx context.Context, opts StreamOptions) (<-chan transcript.UnifiedEntry, error) {
	events, err := StreamEvents(ctx, opts)
	if err != nil {
		return nil, err
	}
	ch := make(chan transcript.UnifiedEntry, 64)
	go func() {
		defer close(ch)
		for event := range events {
			if event.Entry == nil {
				continue
			}
			select {
			case ch <- *event.Entry:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// StreamEvents tails a transcript file and emits entry, checkpoint, and reset
// events on a channel (see Event). The channel closes when ctx is cancelled.
func StreamEvents(ctx context.Context, opts StreamOptions) (<-chan Event, error) {
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
		switch {
		case opts.Provider != "":
			normalizer = NormalizerForProvider(opts.Provider)
		case opts.Discover != nil:
			normalizer = NormalizerForProvider(opts.Discover.Provider)
		default:
			normalizer = transcript.NewClaudeNormalizer()
		}
	}

	ch := make(chan Event, 64)

	go func() {
		defer close(ch)
		tailFile(ctx, transcriptPath, normalizer, opts.StartOffset, ch)
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
			// An unsupported provider will never produce a transcript file;
			// fail fast instead of polling until the timeout masks the cause.
			if errors.Is(err, ErrUnsupportedProvider) {
				return "", err
			}
		}
	}
}

// tailFile reads a JSONL file, tailing it for new content.
func tailFile(ctx context.Context, path string, normalizer transcript.Normalizer, startOffset int64, ch chan<- Event) {
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

	// offset counts bytes of fully-consumed ('\n'-terminated) lines only; a
	// partial trailing line accumulates in pending and is not counted until
	// its newline arrives, so checkpoints always land on a line boundary.
	var offset int64
	if startOffset > 0 {
		if resumableAt(file, startOffset) {
			offset = startOffset
		} else {
			// The file shrank or was rewritten under the saved offset: the
			// resume point is meaningless, so restart from the top and tell
			// the consumer to discard what it cached from earlier streams.
			select {
			case ch <- Event{Reset: true}:
			case <-ctx.Done():
				return
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return
			}
		}
	}

	reader := bufio.NewReader(file)
	checkpoint := offset
	var pending []byte

	for {
		select {
		case <-ctx.Done():
			// Flush any remaining buffered entries
			if flusher, ok := normalizer.(Flusher); ok {
				for _, entry := range flusher.Flush() {
					select {
					case ch <- Event{Entry: entry}:
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
				// Keep any partially-written trailing line for the next round
				// instead of dropping the bytes bufio already consumed.
				if len(line) > 0 {
					pending = append(pending, line...)
				}

				// Flush on EOF (tool calls may be buffered waiting for results)
				if flusher, ok := normalizer.(Flusher); ok {
					for _, entry := range flusher.Flush() {
						select {
						case ch <- Event{Entry: entry}:
						case <-ctx.Done():
							return
						}
					}
				}

				// The normalizer just flushed, so every entry for bytes below
				// offset has been emitted: offset is now safe to resume from.
				if offset > checkpoint {
					checkpoint = offset
					select {
					case ch <- Event{Offset: checkpoint}:
					case <-ctx.Done():
						return
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

		if len(pending) > 0 {
			line = append(pending, line...)
			pending = nil
		}
		offset += int64(len(line))

		if len(line) <= 1 {
			continue
		}

		entry, err := normalizer.NormalizeLine(line)
		if err != nil || entry == nil {
			continue
		}

		select {
		case ch <- Event{Entry: entry}:
		case <-ctx.Done():
			return
		}
	}
}

// resumableAt reports whether the file still supports resuming at offset — it
// has not shrunk below it, and the byte before it is a newline (a rewrite that
// kept the file at least as long almost never preserves the exact boundary).
// On success the file position is left at offset.
func resumableAt(file *os.File, offset int64) bool {
	info, err := file.Stat()
	if err != nil || info.Size() < offset {
		return false
	}
	var boundary [1]byte
	if _, err := file.ReadAt(boundary[:], offset-1); err != nil || boundary[0] != '\n' {
		return false
	}
	_, err = file.Seek(offset, io.SeekStart)
	return err == nil
}

// NormalizerForProvider returns the appropriate normalizer for a provider.
// This is THE provider→normalizer routing table: external consumers (flow's
// TUI transcript loaders) select through it instead of constructing a
// normalizer directly, so provider routing has one definition. Unknown/empty
// providers get the Claude normalizer, the historical default.
func NormalizerForProvider(provider string) transcript.Normalizer {
	switch provider {
	case "codex":
		return transcript.NewCodexNormalizer()
	case "opencode":
		return transcript.NewOpenCodeNormalizer()
	case "pi":
		return transcript.NewPiNormalizer()
	default:
		return transcript.NewClaudeNormalizer()
	}
}
