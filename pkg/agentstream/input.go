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
	engine    mux.MuxEngine
}

// InputOption configures input behavior.
type InputOption func(*inputConfig)

// WithSocket sets a dedicated mux socket name (for isolated agents).
func WithSocket(socket string) InputOption {
	return func(c *inputConfig) {
		c.socket = socket
	}
}

// WithEngine pins delivery to a caller-resolved mux engine, bypassing both
// socket resolution and ambient auto-detection.
//
// Callers that already know which multiplexer hosts the target — chiefly
// groved, which routes from a session's recorded mux — must use this. Ambient
// detection reads the *calling* process's environment, and inside a daemon
// that is the daemon's own: a tmux-hosted pane then gets addressed through
// whatever tuimux daemon happens to answer, and the send fails with an opaque
// "session not found" naming a tmux target that tuimux never had.
func WithEngine(engine mux.MuxEngine) InputOption {
	return func(c *inputConfig) {
		c.engine = engine
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

// newMuxEngine creates a MuxEngine, using a caller-supplied engine or a
// specific socket if configured.
// A non-nil error always comes with a nil engine: forwarding the concrete
// *TmuxEngine result straight out would wrap a nil pointer in a non-nil
// interface, and any `engine != nil` guard downstream would then panic.
func newMuxEngine(cfg *inputConfig) (mux.MuxEngine, error) {
	if cfg.engine != nil {
		return cfg.engine, nil
	}
	if cfg.socket != "" {
		engine, err := mux.NewTmuxEngineWithSocket(cfg.socket)
		if err != nil {
			return nil, err
		}
		return engine, nil
	}
	return mux.DetectMuxEngine(context.Background())
}
