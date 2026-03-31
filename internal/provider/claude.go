package provider

import (
	"bufio"
	"context"
	"io"
	"os"
	"time"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/internal/transcript"
)

// ClaudeSource reads and streams Claude JSONL transcript files.
type ClaudeSource struct{}

func NewClaudeSource() *ClaudeSource {
	return &ClaudeSource{}
}

func (s *ClaudeSource) Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error) {
	file, err := os.Open(info.LogFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	normalizer := transcript.NewClaudeNormalizer()
	entries := scanNormalizeRange(file, normalizer, opts.StartLine, opts.EndLine)

	// Flush buffered tool calls
	for _, entry := range normalizer.Flush() {
		entries = append(entries, *entry)
	}

	return entries, nil
}

func (s *ClaudeSource) Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error) {
	file, err := os.Open(info.LogFilePath)
	if err != nil {
		return nil, err
	}

	// Seek to end to start tailing
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return nil, err
	}

	ch := make(chan transcript.UnifiedEntry, 100)
	normalizer := transcript.NewClaudeNormalizer()

	go func() {
		defer close(ch)
		defer file.Close()

		reader := bufio.NewReader(file)
		for {
			line, err := reader.ReadBytes('\n')
			if err == io.EOF {
				// Flush any buffered entries (e.g. tool calls waiting for results).
				// In streaming mode we emit eagerly rather than waiting for tool results.
				for _, flushed := range normalizer.Flush() {
					select {
					case ch <- *flushed:
					case <-ctx.Done():
						return
					}
				}

				// Check if file still exists
				if _, statErr := os.Stat(info.LogFilePath); statErr != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
					continue
				}
			}
			if err != nil {
				return
			}

			if len(line) > 0 {
				if entry, normErr := normalizer.NormalizeLine(line); normErr == nil && entry != nil {
					select {
					case ch <- *entry:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return ch, nil
}

// scanNormalizeRange reads lines from a reader within a line range and normalizes them.
// startLine and endLine are zero-based line indices. endLine < 0 means read to end.
func scanNormalizeRange(r io.Reader, normalizer transcript.Normalizer, startLine, endLine int) []transcript.UnifiedEntry {
	scanner := bufio.NewScanner(r)
	const maxScanTokenSize = 1024 * 1024 // 1MB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScanTokenSize)

	var entries []transcript.UnifiedEntry
	lineIndex := 0
	for scanner.Scan() {
		if endLine >= 0 && lineIndex >= endLine {
			break
		}
		if lineIndex >= startLine {
			line := scanner.Bytes()
			if len(line) > 0 {
				if entry, err := normalizer.NormalizeLine(line); err == nil && entry != nil {
					entries = append(entries, *entry)
				}
			}
		}
		lineIndex++
	}
	return entries
}
