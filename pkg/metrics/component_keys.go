package metrics

import (
	"fmt"
	"strings"
)

// ComponentVocabulary is the D10 component vocabulary: the subsystems a config
// vector can hold a rendered-artifact hash for.
//
// "briefing" is claude-only but is listed here because the vocabulary is
// provider-neutral; a pi session simply never carries one.
var ComponentVocabulary = []string{
	"prompt",
	"context",
	"memory",
	"skills",
	"plan",
	"briefing",
}

// IsKnownComponent reports whether name is in the D10 vocabulary.
func IsKnownComponent(name string) bool {
	for _, c := range ComponentVocabulary {
		if c == name {
			return true
		}
	}
	return false
}

// ValidateComponentMetricKey enforces the D8 key grammar for
// ComponentMetrics: EXACTLY two dot-separated segments, "<component>.<metric>",
// with a snake_case metric segment.
//
// This is the single definition of that grammar. Both the pi lift (which drops
// offending keys from a session) and the metrics CLI (which validates a
// --by-config argument) call it, deliberately, rather than each carrying its own
// copy that could drift.
//
// The component segment is NOT restricted to ComponentVocabulary here: an
// emitter may legitimately report against a component this build predates, and
// rejecting it would lose data that is otherwise well-formed. Callers that need
// vocabulary enforcement compose this with IsKnownComponent.
func ValidateComponentMetricKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is empty")
	}
	parts := strings.Split(key, ".")
	if len(parts) != 2 {
		return fmt.Errorf("key must have exactly two dot-separated segments, got %d", len(parts))
	}
	component, metric := parts[0], parts[1]
	if component == "" {
		return fmt.Errorf("component segment is empty")
	}
	if metric == "" {
		return fmt.Errorf("metric segment is empty")
	}
	if !isSnakeCase(component) {
		return fmt.Errorf("component segment %q is not snake_case", component)
	}
	if !isSnakeCase(metric) {
		return fmt.Errorf("metric segment %q is not snake_case", metric)
	}
	return nil
}

// isSnakeCase accepts lowercase letters, digits and underscores, and requires a
// leading lowercase letter.
func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
}
