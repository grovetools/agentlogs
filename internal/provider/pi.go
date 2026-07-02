package provider

import (
	"bufio"
	"context"
	"io"
	"os"
	"time"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// PiSource reads and streams pi coding agent session JSONL files.
//
// pi session files are append-only trees (id/parentId branching in-file), so
// full reads MUST linearize along the active branch instead of emitting lines
// in file order — see transcript.NormalizePiFile. Streaming tails per-line,
// which is correct for a live session because appends always extend the
// active branch.
type PiSource struct{}

func NewPiSource() *PiSource {
	return &PiSource{}
}

func (s *PiSource) Read(ctx context.Context, info *session.SessionInfo, opts ReadOptions) ([]transcript.UnifiedEntry, error) {
	file, err := os.Open(info.LogFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entries, err := transcript.NormalizePiFile(file)
	if err != nil {
		return nil, err
	}

	// StartLine/EndLine index the linearized conversation (raw file line
	// numbers are meaningless after tree linearization).
	start := opts.StartLine
	if start < 0 {
		start = 0
	}
	if start > len(entries) {
		start = len(entries)
	}
	end := len(entries)
	if opts.EndLine >= 0 && opts.EndLine < end {
		end = opts.EndLine
	}
	if end < start {
		end = start
	}
	return entries[start:end], nil
}

func (s *PiSource) Stream(ctx context.Context, info *session.SessionInfo) (<-chan transcript.UnifiedEntry, error) {
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
	normalizer := transcript.NewPiNormalizer()

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
