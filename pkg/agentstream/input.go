package agentstream

import (
	"context"

	"github.com/grovetools/core/pkg/tmux"
)

type inputConfig struct {
	socket string
}

// InputOption configures input behavior.
type InputOption func(*inputConfig)

// WithSocket sets a dedicated tmux socket name (for isolated agents using tmux -L).
func WithSocket(socket string) InputOption {
	return func(c *inputConfig) {
		c.socket = socket
	}
}

// SendInput sends keystrokes to an agent's tmux session.
// It first sends Escape + "i" to ensure the agent is in insert mode,
// then sends the input text, followed by C-m (Enter).
func SendInput(ctx context.Context, tmuxTarget string, input string, opts ...InputOption) error {
	cfg := &inputConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	client, err := newTmuxClient(cfg)
	if err != nil {
		return err
	}

	// Escape to normal mode, then "i" to enter insert mode (vim-style agents)
	if err := client.SendKeys(ctx, tmuxTarget, "Escape", "i", input); err != nil {
		return err
	}
	// Submit with Enter
	return client.SendKeys(ctx, tmuxTarget, "C-m")
}

// newTmuxClient creates a tmux client with optional socket.
func newTmuxClient(cfg *inputConfig) (*tmux.Client, error) {
	if cfg.socket != "" {
		return tmux.NewClientWithSocket(cfg.socket)
	}
	return tmux.NewClient()
}
