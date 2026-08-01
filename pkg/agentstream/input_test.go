package agentstream

import (
	"context"
	"errors"
	"testing"

	"github.com/grovetools/core/pkg/mux"
)

// fakeEngine records SendKeys calls. The embedded interface satisfies the rest
// of MuxEngine; any other method would panic, which is the point — SendInput
// must touch nothing else.
type fakeEngine struct {
	mux.MuxEngine
	target string
	keys   []string
	err    error
}

func (f *fakeEngine) SendKeys(_ context.Context, target string, keys ...string) error {
	f.target = target
	f.keys = append([]string(nil), keys...)
	return f.err
}

// WithEngine is the seam groved uses to route input to a session's *recorded*
// mux instead of whatever multiplexer the daemon's own environment implies.
func TestSendInputWithEngineBypassesDetection(t *testing.T) {
	engine := &fakeEngine{}

	if err := SendInput(context.Background(), "proj:job-x", "hello", WithEngine(engine), WithInputMode("standard")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	if engine.target != "proj:job-x" {
		t.Fatalf("target = %q, want proj:job-x", engine.target)
	}
	want := []string{"hello", "Enter"}
	if len(engine.keys) != len(want) {
		t.Fatalf("keys = %v, want %v", engine.keys, want)
	}
	for i := range want {
		if engine.keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", engine.keys, want)
		}
	}
}

func TestSendInputWithEngineVimMode(t *testing.T) {
	engine := &fakeEngine{}

	if err := SendInput(context.Background(), "proj:job-x", "hello", WithEngine(engine)); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	want := []string{"Escape", "i", "hello", "Enter"}
	if len(engine.keys) != len(want) {
		t.Fatalf("keys = %v, want %v", engine.keys, want)
	}
	for i := range want {
		if engine.keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", engine.keys, want)
		}
	}
}

func TestSendInputWithEnginePropagatesSendKeysError(t *testing.T) {
	sentinel := errors.New("pane is gone")
	engine := &fakeEngine{err: sentinel}

	err := SendInput(context.Background(), "proj:job-x", "hello", WithEngine(engine))
	if !errors.Is(err, sentinel) {
		t.Fatalf("SendInput error = %v, want %v", err, sentinel)
	}
}

// A caller-supplied engine outranks a socket name: groved resolves the engine
// (socket included) before it decides the session is tmux-hosted at all.
func TestWithEngineOutranksWithSocket(t *testing.T) {
	engine := &fakeEngine{}

	if err := SendInput(context.Background(), "main:0", "hi", WithSocket("flow-job-nonexistent"), WithEngine(engine)); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	if engine.target != "main:0" {
		t.Fatalf("target = %q, want main:0 — the socket path was taken instead of the engine", engine.target)
	}
}
