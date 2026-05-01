package agentstream

import (
	"context"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/mux"
)

var inputLog = logging.NewUnifiedLogger("agentstream.input")

type inputConfig struct {
	socket string
}

// InputOption configures input behavior.
type InputOption func(*inputConfig)

// WithSocket sets a dedicated mux socket name (for isolated agents).
func WithSocket(socket string) InputOption {
	return func(c *inputConfig) {
		c.socket = socket
	}
}

// SendInput sends keystrokes to an agent's mux session as a single
// atomic send-keys invocation: Escape → i (enter insert mode) → text →
// Enter (submit).
func SendInput(ctx context.Context, tmuxTarget, input string, opts ...InputOption) error {
	cfg := &inputConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	inputLog.Debug("SendInput called").
		Field("target", tmuxTarget).
		Field("socket", cfg.socket).
		Field("input_len", len(input)).
		Log(ctx)

	engine, err := newMuxEngine(cfg)
	if err != nil {
		inputLog.Error("Failed to create mux engine").
			Err(err).
			Field("socket", cfg.socket).
			Log(ctx)
		return err
	}

	if err := engine.SendKeys(ctx, tmuxTarget, "Escape", "i", input, "Enter"); err != nil {
		inputLog.Error("send-keys failed").
			Err(err).
			Field("target", tmuxTarget).
			Field("socket", cfg.socket).
			Log(ctx)
		return err
	}

	inputLog.Debug("SendInput completed").
		Field("target", tmuxTarget).
		Log(ctx)
	return nil
}

// newMuxEngine creates a MuxEngine, using a specific socket if configured.
func newMuxEngine(cfg *inputConfig) (mux.MuxEngine, error) {
	if cfg.socket != "" {
		return mux.NewTmuxEngineWithSocket(cfg.socket)
	}
	return mux.DetectMuxEngine(context.Background())
}
