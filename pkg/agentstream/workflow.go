package agentstream

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// StreamWorkflow tails all workflow runs under a Claude Code session
// directory and fans the normalized entries into a single channel tagged by
// AgentID.
//
// It polls sessionDir/subagents/workflows/ for wf_* run directories. For each
// run it tails journal.jsonl (started/result scoreboard events, normalized by
// transcript.JournalNormalizer) and every agent-<id>.jsonl transcript
// (normalized by a per-file transcript.ClaudeNormalizer). Files that appear
// after streaming starts are picked up by the same polling loop. The returned
// channel closes when ctx is cancelled.
func StreamWorkflow(ctx context.Context, sessionDir string) (<-chan transcript.UnifiedEntry, error) {
	if _, err := os.Stat(sessionDir); err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	workflowsDir := filepath.Join(sessionDir, "subagents", "workflows")

	out := make(chan transcript.UnifiedEntry, 64)

	go func() {
		defer close(out)

		var wg sync.WaitGroup
		seen := make(map[string]bool) // file paths already being tailed
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			discoverWorkflowFiles(workflowsDir, seen, func(path, agentID string, normalizer transcript.Normalizer) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					tailTagged(ctx, path, agentID, normalizer, out)
				}()
			})

			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case <-ticker.C:
			}
		}
	}()

	return out, nil
}

// discoverWorkflowFiles scans workflowsDir for wf_* run directories and calls
// start for each journal.jsonl and agent-*.jsonl not yet in seen. The
// directory not existing yet is normal (the workflow may not have started).
func discoverWorkflowFiles(workflowsDir string, seen map[string]bool, start func(path, agentID string, normalizer transcript.Normalizer)) {
	runs, err := os.ReadDir(workflowsDir)
	if err != nil {
		return
	}

	for _, run := range runs {
		if !run.IsDir() || !strings.HasPrefix(run.Name(), "wf_") {
			continue
		}
		runDir := filepath.Join(workflowsDir, run.Name())

		journalPath := filepath.Join(runDir, "journal.jsonl")
		if !seen[journalPath] {
			if _, err := os.Stat(journalPath); err == nil {
				seen[journalPath] = true
				start(journalPath, "", transcript.NewJournalNormalizer())
			}
		}

		files, err := os.ReadDir(runDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			path := filepath.Join(runDir, name)
			if seen[path] {
				continue
			}
			seen[path] = true
			agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
			// Normalizers are stateful (tool-call buffering), so each
			// tailed file gets its own instance.
			start(path, agentID, transcript.NewClaudeNormalizer())
		}
	}
}

// tailTagged runs tailFile into a private channel and forwards entries to
// out, tagging them with agentID when the normalizer didn't set one.
func tailTagged(ctx context.Context, path, agentID string, normalizer transcript.Normalizer, out chan<- transcript.UnifiedEntry) {
	inner := make(chan transcript.UnifiedEntry, 16)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for entry := range inner {
			if entry.AgentID == "" {
				entry.AgentID = agentID
			}
			select {
			case out <- entry:
			case <-ctx.Done():
				return
			}
		}
	}()

	tailFile(ctx, path, normalizer, inner)
	close(inner)
	<-done
}
