package agentstream

import (
	"context"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/tmux"
)

var inputLog = logging.NewUnifiedLogger("agentstream.input")

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

	inputLog.Debug("SendInput called").
		Field("tmux_target", tmuxTarget).
		Field("socket", cfg.socket).
		Field("input_len", len(input)).
		Log(ctx)

	client, err := newTmuxClient(cfg)
	if err != nil {
		inputLog.Error("Failed to create tmux client").
			Err(err).
			Field("socket", cfg.socket).
			Log(ctx)
		return err
	}

	// Escape to normal mode, then "i" to enter insert mode (vim-style agents)
	if err := client.SendKeys(ctx, tmuxTarget, "Escape", "i", input); err != nil {
		inputLog.Error("tmux send-keys (Escape+i+text) failed").
			Err(err).
			Field("tmux_target", tmuxTarget).
			Field("socket", cfg.socket).
			Log(ctx)
		return err
	}
	// Submit with Enter
	if err := client.SendKeys(ctx, tmuxTarget, "C-m"); err != nil {
		inputLog.Error("tmux send-keys (C-m submit) failed").
			Err(err).
			Field("tmux_target", tmuxTarget).
			Field("socket", cfg.socket).
			Log(ctx)
		return err
	}

	inputLog.Debug("SendInput completed").
		Field("tmux_target", tmuxTarget).
		Log(ctx)
	return nil
}

// newTmuxClient creates a tmux client with optional socket.
func newTmuxClient(cfg *inputConfig) (*tmux.Client, error) {
	if cfg.socket != "" {
		return tmux.NewClientWithSocket(cfg.socket)
	}
	return tmux.NewClient()
}
