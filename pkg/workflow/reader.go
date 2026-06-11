// Package workflow provides batch parsing of Claude Code workflow run
// directories (wf_*): the journal scoreboard, the persisted script's meta,
// and per-agent transcripts, in one structured read.
package workflow

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/agentlogs/pkg/transcript"
	"github.com/grovetools/core/pkg/workflows"
)

// journalBufferSize is the max journal/transcript line size. Journal result
// events embed full agent results on a single line and can be far larger
// than bufio.Scanner's 64KB default token limit.
const journalBufferSize = 16 * 1024 * 1024

// AgentRun is one agent's record within a workflow run.
type AgentRun struct {
	// Started is true when the journal recorded a "started" event.
	Started bool
	// Result is the raw result payload from the journal's "result" event,
	// preserved structurally (may be a string, object, etc). Nil when the
	// agent never returned a result.
	Result json.RawMessage
	// Entries are the agent's normalized transcript entries, populated when
	// the run dir contains agent-<id>.jsonl.
	Entries []transcript.UnifiedEntry
}

// WorkflowRun is the parsed content of one wf_* run directory.
type WorkflowRun struct {
	// RunID is the run directory name, e.g. "wf_4650c05a-c39".
	RunID string
	// Meta is the parsed script meta when the persisted orchestration
	// script was found in one of the scripts dirs; nil otherwise.
	Meta *workflows.ScriptMeta
	// Agents maps agent ID to its run record.
	Agents map[string]*AgentRun
}

// ReadWorkflowRun parses one workflow run directory. scriptsDirs are
// searched in order for the run's persisted script (<name>-<runId>.js);
// session artifacts fragment across project-slug dirs, so callers pass every
// candidate workflows/scripts/ dir. A missing script or malformed
// journal/transcript lines degrade silently (the journal format is
// undocumented Claude Code internals); only an unreadable run directory or
// journal I/O failure is an error.
func ReadWorkflowRun(runDir string, scriptsDirs []string) (*WorkflowRun, error) {
	if _, err := os.Stat(runDir); err != nil {
		return nil, fmt.Errorf("workflow run directory: %w", err)
	}

	run := &WorkflowRun{
		RunID:  filepath.Base(runDir),
		Agents: make(map[string]*AgentRun),
	}

	for _, dir := range scriptsDirs {
		if meta := workflows.LoadRunMeta(dir, run.RunID); meta != nil {
			run.Meta = meta
			break
		}
	}

	if err := readJournal(filepath.Join(runDir, "journal.jsonl"), run); err != nil {
		return nil, err
	}

	files, err := os.ReadDir(runDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list workflow run directory: %w", err)
	}
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		entries, err := readAgentTranscript(filepath.Join(runDir, name), agentID)
		if err != nil {
			// A single unreadable transcript shouldn't sink the run.
			continue
		}
		run.agent(agentID).Entries = entries
	}

	return run, nil
}

// agent returns the run's record for agentID, creating it when absent
// (an agent transcript can exist before its journal "started" event lands).
func (r *WorkflowRun) agent(agentID string) *AgentRun {
	if a, ok := r.Agents[agentID]; ok {
		return a
	}
	a := &AgentRun{}
	r.Agents[agentID] = a
	return a
}

// readJournal folds journal.jsonl started/result events into run.Agents.
// A missing journal is tolerated (the run may have been interrupted before
// any event was written); malformed lines and unknown event types are
// skipped for format-drift tolerance.
func readJournal(path string, run *WorkflowRun) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to open journal: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), journalBufferSize)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type    string          `json:"type"`
			AgentID string          `json:"agentId"`
			Result  json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.AgentID == "" {
			continue
		}
		switch ev.Type {
		case "started":
			run.agent(ev.AgentID).Started = true
		case "result":
			run.agent(ev.AgentID).Result = ev.Result
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read journal: %w", err)
	}
	return nil
}

// readAgentTranscript batch-normalizes one agent-<id>.jsonl with a fresh
// ClaudeNormalizer (normalizers are stateful: tool-call buffering), tagging
// entries with agentID when the transcript line didn't carry one. Malformed
// lines are skipped.
func readAgentTranscript(path, agentID string) ([]transcript.UnifiedEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	normalizer := transcript.NewClaudeNormalizer()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), journalBufferSize)

	var entries []transcript.UnifiedEntry
	add := func(entry *transcript.UnifiedEntry) {
		if entry.AgentID == "" {
			entry.AgentID = agentID
		}
		entries = append(entries, *entry)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		entry, err := normalizer.NormalizeLine(line)
		if err != nil || entry == nil {
			continue
		}
		add(entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, entry := range normalizer.Flush() {
		add(entry)
	}
	return entries, nil
}
