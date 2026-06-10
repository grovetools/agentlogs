package agentstream

import (
	"errors"
	"strings"
	"testing"
)

func TestDiscoverTranscript_OpencodeNotImplemented(t *testing.T) {
	_, err := DiscoverTranscript(DiscoverOptions{Provider: "opencode", WorkDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for opencode transcript discovery, got nil")
	}
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Errorf("expected ErrUnsupportedProvider, got: %v", err)
	}
	for _, want := range []string{"opencode", "claude", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestDiscoverTranscript_UnknownProvider(t *testing.T) {
	_, err := DiscoverTranscript(DiscoverOptions{Provider: "gemini"})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Errorf("expected ErrUnsupportedProvider, got: %v", err)
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error should name the provider, got: %v", err)
	}
}
