package agentstream

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AgentStatus represents the parsed status of an agent session from tmux pane output.
type AgentStatus struct {
	State       string     // "running" or "idle"
	RawLine     string     // The raw status line (cleaned of escape hints)
	Activity    string     // e.g., "Unravelling", "Building and verifying changes"
	Duration    string     // e.g., "8s", "1m 58s"
	TokenFlow   string     // "↑" (upload) or "↓" (download)
	DeltaTokens string     // e.g., "220", "4.9k"
	TotalTokens int        // e.g., 133990
	TodoItems   []TodoItem // Parsed todo items
	LastUpdate  time.Time  // When this status was captured
}

// TodoItem represents a single item in an agent's todo list.
type TodoItem struct {
	Text      string // The todo item text
	Completed bool   // Whether the item is checked (☒) or unchecked (☐)
}

var (
	// Match total tokens line ending with "NNN tokens"
	totalTokensRe = regexp.MustCompile(`(\d+)\s+tokens\s*$`)

	// Match status line: icon + activity + parenthetical details
	// e.g., "✶ Unravelling… (esc to interrupt · 8s · ↓ 220 tokens · thinking)"
	statusLineRe = regexp.MustCompile(`^\s*([✶·✻])\s+(.+?)…\s+\((.+)\)\s*$`)

	// Match duration patterns like "8s", "1m 58s", "2m", "1h 2m"
	durationRe = regexp.MustCompile(`\b(\d+[smh](?:\s*\d+[smh])?)\b`)

	// Match token delta patterns like "↑ 4.9k tokens" or "↓ 220 tokens"
	tokenDeltaRe = regexp.MustCompile(`([↑↓])\s+([\d.]+k?)\s+tokens`)

	// Match todo item lines: optional "⎿" prefix, then ☒/☐ checkbox
	todoItemRe = regexp.MustCompile(`^\s*(?:⎿\s+)?([☒☐])\s+(.+?)\s*$`)
)

// ParsePaneOutput parses raw tmux pane output to extract agent session status.
// It scans from the bottom up, looking for the status line and token count.
// Returns nil if nothing useful is found.
func ParsePaneOutput(output string) *AgentStatus {
	lines := strings.Split(output, "\n")

	scanLimit := 30
	if len(lines) < scanLimit {
		scanLimit = len(lines)
	}

	status := &AgentStatus{
		LastUpdate: time.Now(),
	}

	var foundStatusLine bool
	var foundTotalTokens bool
	var inputPromptLineIndex int = -1
	var statusLineIndex int = -1

	for i := len(lines) - 1; i >= len(lines)-scanLimit && i >= 0; i-- {
		line := lines[i]

		if !foundTotalTokens {
			if matches := totalTokensRe.FindStringSubmatch(line); len(matches) > 1 {
				if tokens, err := strconv.Atoi(matches[1]); err == nil {
					status.TotalTokens = tokens
					foundTotalTokens = true
				}
			}
		}

		if inputPromptLineIndex < 0 {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "⏵⏵") || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, ">> ") {
				inputPromptLineIndex = i
			}
		}

		if !foundStatusLine {
			if matches := statusLineRe.FindStringSubmatch(line); len(matches) > 0 {
				activity := matches[2]
				parenBlock := matches[3]

				status.State = "running"
				rawLine := strings.TrimSpace(line)
				rawLine = strings.Replace(rawLine, "esc to interrupt · ", "", 1)
				rawLine = strings.Replace(rawLine, "ctrl+t to hide todos · ", "", 1)
				status.RawLine = rawLine
				status.Activity = strings.TrimSpace(activity)

				if durMatch := durationRe.FindStringSubmatch(parenBlock); len(durMatch) > 1 {
					status.Duration = durMatch[1]
				}
				if tokenMatch := tokenDeltaRe.FindStringSubmatch(parenBlock); len(tokenMatch) > 2 {
					status.TokenFlow = tokenMatch[1]
					status.DeltaTokens = tokenMatch[2]
				}

				foundStatusLine = true
				statusLineIndex = i
			}
		}

		if foundStatusLine && foundTotalTokens {
			break
		}
	}

	// Scan forward from status line to find todo items
	if statusLineIndex >= 0 {
		for i := statusLineIndex + 1; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "⏵⏵") || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, ">> ") {
				break
			}

			if matches := todoItemRe.FindStringSubmatch(line); len(matches) > 2 {
				status.TodoItems = append(status.TodoItems, TodoItem{
					Text:      matches[2],
					Completed: matches[1] == "☒",
				})
			} else if len(status.TodoItems) > 0 {
				break
			}
		}
	}

	if !foundStatusLine && inputPromptLineIndex >= 0 {
		status.State = "idle"
		status.Activity = "Waiting for input..."
	}

	if status.State == "" && status.TotalTokens == 0 {
		return nil
	}

	return status
}

// StateIcon returns the appropriate icon for the agent state.
func (s *AgentStatus) StateIcon() string {
	switch s.State {
	case "running":
		return "●"
	case "idle":
		return "○"
	default:
		return "?"
	}
}

// CaptureOption configures pane capture behavior.
type CaptureOption func(*inputConfig)

// WithCaptureSocket sets a dedicated tmux socket for capture.
func WithCaptureSocket(socket string) CaptureOption {
	return func(c *inputConfig) {
		c.socket = socket
	}
}

// CapturePane runs tmux capture-pane and returns raw output.
func CapturePane(ctx context.Context, tmuxTarget string, opts ...CaptureOption) (string, error) {
	cfg := &inputConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	client, err := newTmuxClient(cfg)
	if err != nil {
		return "", err
	}

	return client.CapturePane(ctx, tmuxTarget)
}
