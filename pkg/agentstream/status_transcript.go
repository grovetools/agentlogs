package agentstream

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// transcriptActiveWindow is the recency threshold for transcript-derived
// status: a transcript that grew within this window is considered a running
// session; older is idle. Coding-agent turns routinely include multi-minute
// tool calls, so this is deliberately generous.
const transcriptActiveWindow = 2 * time.Minute

// transcriptStatusTailBytes bounds how much of the transcript tail is parsed
// for the activity label / in-flight tool detection. Recency itself comes
// from the file mtime, so a bounded tail is enough.
const transcriptStatusTailBytes = 512 * 1024

// DeriveTranscriptStatus derives an AgentStatus from transcript activity for
// providers whose interactive TUI grove does not scrape — everything except
// Claude (see StatusFromPane). It is intentionally coarser than the Claude
// pane parse: State is "running" while the transcript file is still growing
// (mtime within transcriptActiveWindow), "idle" otherwise; Activity names the
// in-flight tool call when the tail ends with a tool_call that has no
// matching tool_result, else the kind of the last entry. The pane-scrape-only
// fields (RawLine, Duration, TokenFlow, DeltaTokens, TotalTokens, TodoItems)
// stay zero — they have no transcript equivalent.
//
// path is the provider's transcript file (claude/codex/pi JSONL). Providers
// without a transcript file (opencode) get a stat-only status when path
// points at their session info file: recency still works (the info file's
// mtime tracks session updates), the activity label degrades to "".
func DeriveTranscriptStatus(path, provider string, now time.Time) (*AgentStatus, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("transcript not readable for status derivation: %w", err)
	}

	status := &AgentStatus{
		State:      "idle",
		LastUpdate: now,
	}
	if now.Sub(fi.ModTime()) < transcriptActiveWindow {
		status.State = "running"
	}

	activity, inFlight := transcriptTailActivity(path, provider)
	switch {
	case inFlight != "":
		status.Activity = "tool: " + inFlight
		// An unresolved tool call means the agent is mid-turn even when the
		// tool has been quiet longer than the recency window (long builds).
		status.State = "running"
	case activity != "":
		status.Activity = activity
	case status.State == "idle":
		status.Activity = "Waiting for input..."
	}
	return status, nil
}

// transcriptTailActivity normalizes the transcript's tail and reports the
// last entry's activity label plus the name of an in-flight tool call (a
// tool_call with no later tool_result), if any. Best-effort: any parse
// trouble returns empty strings.
func transcriptTailActivity(path, provider string) (activity, inFlightTool string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	if fi, err := f.Stat(); err == nil && fi.Size() > transcriptStatusTailBytes {
		if _, err := f.Seek(fi.Size()-transcriptStatusTailBytes, io.SeekStart); err != nil {
			return "", ""
		}
	}

	normalizer := NormalizerForProvider(provider)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	first := true

	pendingTools := make(map[string]string) // tool_call id -> name
	var order []string                      // ids in call order
	for scanner.Scan() {
		line := scanner.Bytes()
		if first {
			// After a mid-file seek the first line is almost certainly torn;
			// normalizers tolerate garbage, but skip it outright.
			first = false
			if !strings.HasPrefix(strings.TrimSpace(string(line)), "{") {
				continue
			}
		}
		if len(line) == 0 {
			continue
		}
		entry, err := normalizer.NormalizeLine(line)
		if err != nil || entry == nil {
			continue
		}
		activity = entryActivityLabel(entry)
		for _, part := range entry.Parts {
			switch part.Type {
			case "tool_call":
				if tc, ok := part.Content.(transcript.UnifiedToolCall); ok {
					if _, seen := pendingTools[tc.ID]; !seen {
						order = append(order, tc.ID)
					}
					pendingTools[tc.ID] = tc.Name
				}
			case "tool_result":
				if tr, ok := part.Content.(transcript.UnifiedToolResult); ok {
					delete(pendingTools, tr.ToolCallID)
				}
			}
		}
	}
	// Buffered normalizers (claude) may hold tool calls awaiting results —
	// exactly the in-flight ones.
	if flusher, ok := normalizer.(Flusher); ok {
		for _, entry := range flusher.Flush() {
			for _, part := range entry.Parts {
				if part.Type == "tool_call" {
					if tc, ok := part.Content.(transcript.UnifiedToolCall); ok {
						if _, seen := pendingTools[tc.ID]; !seen {
							order = append(order, tc.ID)
						}
						pendingTools[tc.ID] = tc.Name
					}
				}
			}
		}
	}

	// Newest unresolved tool call wins.
	for i := len(order) - 1; i >= 0; i-- {
		if name, ok := pendingTools[order[i]]; ok {
			return activity, name
		}
	}
	return activity, ""
}

// entryActivityLabel renders a short label for a normalized entry: the kind
// of work its last part represents.
func entryActivityLabel(entry *transcript.UnifiedEntry) string {
	if entry == nil || len(entry.Parts) == 0 {
		return ""
	}
	last := entry.Parts[len(entry.Parts)-1]
	switch last.Type {
	case "tool_call":
		if tc, ok := last.Content.(transcript.UnifiedToolCall); ok && tc.Name != "" {
			return "tool: " + tc.Name
		}
		return "calling tool"
	case "tool_result":
		return "processing tool result"
	case "reasoning":
		return "thinking"
	default:
		if entry.Role == "assistant" {
			return "responding"
		}
		return "reading input"
	}
}
