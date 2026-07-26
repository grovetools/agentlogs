package display

import (
	"strings"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

func TestToolCallSummary(t *testing.T) {
	longPath := "/Users/test/a/very/long/path/to/project/internal/component/file.go"
	tests := []struct {
		name string
		tool transcript.UnifiedToolCall
		want string
	}{
		{"bash string", transcript.UnifiedToolCall{Name: "bash", Input: map[string]interface{}{"command": "go test ./..."}}, "Bash(go test ./...)"},
		{"codex argv", transcript.UnifiedToolCall{Name: "shell", Input: map[string]interface{}{"command": []interface{}{"bash", "-lc", "go test ./..."}}}, "Shell(go test ./...)"},
		{"long path", transcript.UnifiedToolCall{Name: "Read", Input: map[string]interface{}{"file_path": longPath}}, "Read("},
		{"query truncation", transcript.UnifiedToolCall{Name: "WebSearch", Input: map[string]interface{}{"query": strings.Repeat("x", 50)}}, "WebSearch(" + strings.Repeat("x", 37) + "...)"},
		{"title fallback", transcript.UnifiedToolCall{Name: "task", Title: "Explore code", Input: map[string]interface{}{}}, "Task(Explore code)"},
		{"empty input", transcript.UnifiedToolCall{Name: "glob", Input: map[string]interface{}{}}, "Glob"},
		{"multiline title is folded", transcript.UnifiedToolCall{Name: "task", Title: "Explore\ncode", Input: map[string]interface{}{}}, "Task(Explore code)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolCallSummary(tt.tool)
			if strings.ContainsAny(got, "\r\n") || strings.Contains(got, "\x1b[") {
				t.Fatalf("ToolCallSummary() is not plain one-line text: %q", got)
			}
			if tt.name == "long path" {
				if !strings.Contains(got, "file.go") {
					t.Fatalf("ToolCallSummary() = %q", got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("ToolCallSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
