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

// SendInput sends keystrokes to an agent's tmux session as a single
// atomic send-keys invocation: Escape → i (enter insert mode) → text →
// Enter (submit). Two invocations used to race — the second send-keys
// could arrive before tmux finished typing the input, or claude's TUI
// would change modes between them and eat the Enter. Folding into one
// tmux command eliminates the race.
//
// Using "Enter" instead of "C-m" because newer claude code TUIs treat
// them differently in some modes; "Enter" is the keyword the launch
// path uses and is what the rest of grovetools standardized on.
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

	if err := client.SendKeys(ctx, tmuxTarget, "Escape", "i", input, "Enter"); err != nil {
		inputLog.Error("tmux send-keys failed").
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
