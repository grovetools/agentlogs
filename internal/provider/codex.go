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

// CodexSource reads and streams Codex JSONL transcript files.
type CodexSource struct{}

func NewCodexSource() *CodexSource {
	return &CodexSource{}
}

func (s *CodexSource) Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error) {
	file, err := os.Open(info.LogFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	normalizer := transcript.NewCodexNormalizer()
	entries := scanNormalizeRange(file, normalizer, opts.StartLine, opts.EndLine)
	return entries, nil
}

func (s *CodexSource) Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error) {
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
	normalizer := transcript.NewCodexNormalizer()

	go func() {
		defer close(ch)
		defer file.Close()

		reader := bufio.NewReader(file)
		for {
			line, err := reader.ReadBytes('\n')
			if err == io.EOF {
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
