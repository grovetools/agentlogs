package agentstream

import (
	"context"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/mux"
)

var inputLog = logging.NewUnifiedLogger("agentstream.input")

type inputConfig struct {
	socket    string
	inputMode string
}

// InputOption configures input behavior.
type InputOption func(*inputConfig)

// WithSocket sets a dedicated mux socket name (for isolated agents).
func WithSocket(socket string) InputOption {
	return func(c *inputConfig) {
		c.socket = socket
	}
}

// WithInputMode sets the interactive input mode of the target agent's UI:
// "vim" prefixes the text with Escape→i (leave normal mode, enter insert —
// Claude Code's vim keybindings, the historical default), anything else sends
// the text as-is. The per-provider default lives in flow's agent provider
// registry (AgentProviderSpec.DefaultInputMode); pass it through rather than
// re-deriving it here.
func WithInputMode(mode string) InputOption {
	return func(c *inputConfig) {
		c.inputMode = mode
	}
}

// SendInput sends keystrokes to an agent's mux session as a single atomic
// send-keys invocation: in vim input mode (the default, matching Claude
// Code's UI) Escape → i → text → Enter; in standard mode just text → Enter.
func SendInput(ctx context.Context, tmuxTarget, input string, opts ...InputOption) error {
	cfg := &inputConfig{inputMode: "vim"}
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

	keys := []string{input, "Enter"}
	if cfg.inputMode == "vim" {
		keys = append([]string{"Escape", "i"}, keys...)
	}
	if err := engine.SendKeys(ctx, tmuxTarget, keys...); err != nil {
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
