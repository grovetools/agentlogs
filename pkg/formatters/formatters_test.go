package formatters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/grovetools/core/tui/theme"
)

// ansiRE matches SGR escape sequences emitted by lipgloss when a color profile
// is active. Tests normally run without a TTY (so no codes are emitted), but
// stripping keeps the assertions stable regardless of the detected profile.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// fixture loads a raw JSON fixture from testdata/.
func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return json.RawMessage(b)
}

// detailLevels are the values callers pass through the ToolFormatter contract.
// Only FormatWriteTool's Write branch actually reads this argument.
var detailLevels = []string{"full", "brief", "minimal", ""}

// ---------------------------------------------------------------------------
// FormatWriteTool — Edit branch (old_string + new_string)
// ---------------------------------------------------------------------------

// TestFormatWriteToolEditMaxLines walks the maxLines truncation boundary for
// the Edit diff view: 0 and negatives mean "show everything", maxLines >= the
// line count shows everything without an ellipsis line, and maxLines < the line
// count truncates and appends a "... (N more lines ...)" marker. The removed
// and added sides are truncated independently.
func TestFormatWriteToolEditMaxLines(t *testing.T) {
	input := json.RawMessage(`{
		"file_path": "/tmp/a.go",
		"old_string": "old1\nold2\nold3",
		"new_string": "new1\nnew2"
	}`)
	header := theme.IconFile + " Editing /tmp/a.go\n"

	tests := []struct {
		name     string
		maxLines int
		want     string
	}{
		{
			name:     "zero shows everything",
			maxLines: 0,
			want: header +
				"  - old1\n  - old2\n  - old3\n" +
				"  + new1\n  + new2\n",
		},
		{
			name:     "negative also shows everything",
			maxLines: -1,
			want: header +
				"  - old1\n  - old2\n  - old3\n" +
				"  + new1\n  + new2\n",
		},
		{
			name:     "above both counts shows everything",
			maxLines: 10,
			want: header +
				"  - old1\n  - old2\n  - old3\n" +
				"  + new1\n  + new2\n",
		},
		{
			name:     "exactly the removed-line count does not truncate",
			maxLines: 3,
			want: header +
				"  - old1\n  - old2\n  - old3\n" +
				"  + new1\n  + new2\n",
		},
		{
			name:     "truncates removed side only",
			maxLines: 2,
			want: header +
				"  - old1\n  - old2\n  - ... (1 more lines removed)\n" +
				"  + new1\n  + new2\n",
		},
		{
			name:     "truncates both sides",
			maxLines: 1,
			want: header +
				"  - old1\n  - ... (2 more lines removed)\n" +
				"  + new1\n  + ... (1 more lines added)\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plain(FormatWriteTool(input, tc.maxLines, "full"))
			if got != tc.want {
				t.Errorf("FormatWriteTool(maxLines=%d)\n got: %q\nwant: %q", tc.maxLines, got, tc.want)
			}
		})
	}
}

// TestFormatWriteToolEditIgnoresDetailLevel pins that detailLevel is read ONLY
// by the Write branch: the Edit diff renders identically for every level.
func TestFormatWriteToolEditIgnoresDetailLevel(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/tmp/a.go","old_string":"a\nb\nc\nd\ne\nf\ng","new_string":"x"}`)

	base := plain(FormatWriteTool(input, 0, detailLevels[0]))
	for _, lvl := range detailLevels[1:] {
		if got := plain(FormatWriteTool(input, 0, lvl)); got != base {
			t.Errorf("detailLevel %q changed the Edit output\n got: %q\nwant: %q", lvl, got, base)
		}
	}
	// Sanity: the shared output really is the full diff.
	if !strings.Contains(base, "  - g\n") {
		t.Errorf("expected all removed lines in %q", base)
	}
}

// TestFormatWriteToolEditRequiresBothStrings pins a surprising gap: the Edit
// branch fires only when BOTH old_string and new_string are non-empty. A pure
// deletion (new_string: "") or a pure insertion (old_string: "") falls through
// to the Content branch, finds no content, and renders NOTHING — not even the
// file name.
func TestFormatWriteToolEditRequiresBothStrings(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"deletion: empty new_string", `{"file_path":"/tmp/a.go","old_string":"gone","new_string":""}`},
		{"insertion: empty old_string", `{"file_path":"/tmp/a.go","old_string":"","new_string":"added"}`},
		{"only old_string present", `{"file_path":"/tmp/a.go","old_string":"gone"}`},
		{"only new_string present", `{"file_path":"/tmp/a.go","new_string":"added"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatWriteTool(json.RawMessage(tc.input), 0, "full"); got != "" {
				t.Errorf("want empty string (falls through to the default formatter), got %q", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatWriteTool — Write branch (content)
// ---------------------------------------------------------------------------

// TestFormatWriteToolContentDetailLevel pins the Write branch's summarisation
// rule: expand every line when detailLevel == "full" OR the content is <= 5
// lines; otherwise collapse to a "+ (N lines)" summary. Any level other than
// the exact string "full" behaves the same.
func TestFormatWriteToolContentDetailLevel(t *testing.T) {
	short := json.RawMessage(`{"file_path":"/tmp/s.txt","content":"a\nb\nc"}`)
	// Exactly 5 lines - the inclusive boundary.
	five := json.RawMessage(`{"file_path":"/tmp/f.txt","content":"1\n2\n3\n4\n5"}`)
	// 6 lines - one past the boundary.
	six := json.RawMessage(`{"file_path":"/tmp/x.txt","content":"1\n2\n3\n4\n5\n6"}`)

	tests := []struct {
		name        string
		input       json.RawMessage
		detailLevel string
		want        string
	}{
		{
			name: "short content expands at any level", input: short, detailLevel: "brief",
			want: theme.IconFilePlus + " Writing to /tmp/s.txt\n+ a\n+ b\n+ c\n",
		},
		{
			name: "five lines is the inclusive boundary", input: five, detailLevel: "brief",
			want: theme.IconFilePlus + " Writing to /tmp/f.txt\n+ 1\n+ 2\n+ 3\n+ 4\n+ 5\n",
		},
		{
			name: "six lines collapses when not full", input: six, detailLevel: "brief",
			want: theme.IconFilePlus + " Writing to /tmp/x.txt\n+ (6 lines)\n",
		},
		{
			name: "empty detail level behaves like brief", input: six, detailLevel: "",
			want: theme.IconFilePlus + " Writing to /tmp/x.txt\n+ (6 lines)\n",
		},
		{
			name: "unknown detail level behaves like brief", input: six, detailLevel: "verbose",
			want: theme.IconFilePlus + " Writing to /tmp/x.txt\n+ (6 lines)\n",
		},
		{
			name: "full expands past the boundary", input: six, detailLevel: "full",
			want: theme.IconFilePlus + " Writing to /tmp/x.txt\n+ 1\n+ 2\n+ 3\n+ 4\n+ 5\n+ 6\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plain(FormatWriteTool(tc.input, 0, tc.detailLevel))
			if got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestFormatWriteToolContentIgnoresMaxLines pins that maxLines has NO effect on
// the Write (content) branch — only the Edit diff honours it. An 8-line file
// with maxLines=1 still renders all 8 lines at detailLevel "full".
func TestFormatWriteToolContentIgnoresMaxLines(t *testing.T) {
	input := fixture(t, "write_large.json")

	want := theme.IconFilePlus + " Writing to /tmp/example/main.go\n" +
		"+ package main\n" +
		"+ \n" +
		"+ import \"fmt\"\n" +
		"+ \n" +
		"+ func main() {\n" +
		"+ fmt.Println(\"hi\")\n" +
		"+ }\n" +
		"+ \n"

	for _, maxLines := range []int{0, 1, 3, 100} {
		got := plain(FormatWriteTool(input, maxLines, "full"))
		if got != want {
			t.Errorf("maxLines=%d\n got: %q\nwant: %q", maxLines, got, want)
		}
	}

	// The trailing "\n" in the fixture's content yields a final empty line, so
	// the collapsed summary counts 8, not 7.
	got := plain(FormatWriteTool(input, 0, "brief"))
	want = theme.IconFilePlus + " Writing to /tmp/example/main.go\n+ (8 lines)\n"
	if got != want {
		t.Errorf("collapsed\n got: %q\nwant: %q", got, want)
	}
}

// TestFormatWriteToolStripsCommonIndent pins stripCommonIndent's observable
// effect, including its off-by-one quirk: a trailing newline in the content
// produces an EXTRA blank rendered line (2 real lines render as 4).
func TestFormatWriteToolStripsCommonIndent(t *testing.T) {
	t.Run("common indent removed", func(t *testing.T) {
		input := json.RawMessage(`{"file_path":"/tmp/i.txt","content":"    a\n      b\n    c"}`)
		want := theme.IconFilePlus + " Writing to /tmp/i.txt\n+ a\n+   b\n+ c\n"
		if got := plain(FormatWriteTool(input, 0, "full")); got != want {
			t.Errorf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no common indent leaves text untouched", func(t *testing.T) {
		input := json.RawMessage(`{"file_path":"/tmp/i.txt","content":"a\n    b"}`)
		want := theme.IconFilePlus + " Writing to /tmp/i.txt\n+ a\n+     b\n"
		if got := plain(FormatWriteTool(input, 0, "full")); got != want {
			t.Errorf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("trailing newline plus indent adds a spurious blank line", func(t *testing.T) {
		// "    a\n    b\n" is 2 content lines, but stripCommonIndent emits a
		// newline for the trailing empty element AND for the preceding line,
		// so the rendered output has two blank "+ " lines.
		input := json.RawMessage(`{"file_path":"/tmp/i.txt","content":"    a\n    b\n"}`)
		want := theme.IconFilePlus + " Writing to /tmp/i.txt\n+ a\n+ b\n+ \n+ \n"
		if got := plain(FormatWriteTool(input, 0, "full")); got != want {
			t.Errorf("\n got: %q\nwant: %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// FormatWriteTool — malformed / missing-key input
// ---------------------------------------------------------------------------

// TestFormatWriteToolMalformed pins that every unusable payload degrades to the
// empty string (the documented "let the default formatter handle it" path)
// rather than panicking or emitting a partial header.
func TestFormatWriteToolMalformed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"truncated object", `{"file_path":"/tmp/a.go","content":`},
		{"not json", `this is not json`},
		{"empty raw message", ``},
		{"json null", `null`},
		{"json array", `[]`},
		{"json string", `"hello"`},
		{"json number", `42`},
		{"empty object", `{}`},
		{"no recognised keys", `{"foo":"bar","baz":1}`},
		{"file_path only", `{"file_path":"/tmp/a.go"}`},
		{"content is empty string", `{"file_path":"/tmp/a.go","content":""}`},
		{"content wrong type", `{"file_path":"/tmp/a.go","content":123}`},
		{"file_path wrong type", `{"file_path":99,"content":"a"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, lvl := range detailLevels {
				if got := FormatWriteTool(json.RawMessage(tc.input), 0, lvl); got != "" {
					t.Errorf("detailLevel %q: want empty string, got %q", lvl, got)
				}
			}
		})
	}
}

// TestFormatWriteToolMissingFilePathStillRenders pins that an absent file_path
// is NOT treated as malformed — the header is rendered with an empty path.
func TestFormatWriteToolMissingFilePathStillRenders(t *testing.T) {
	got := plain(FormatWriteTool(json.RawMessage(`{"content":"a"}`), 0, "full"))
	want := theme.IconFilePlus + " Writing to \n+ a\n"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// FormatReadTool
// ---------------------------------------------------------------------------

// TestFormatReadTool is table-driven over the offset/limit rendering matrix.
// Only strictly-positive offset/limit values appear in the parenthetical.
func TestFormatReadTool(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "path only", input: `{"file_path":"/tmp/a.go"}`,
			want: theme.IconFile + " Reading /tmp/a.go\n",
		},
		{
			name: "offset only", input: `{"file_path":"/tmp/a.go","offset":10}`,
			want: theme.IconFile + " Reading /tmp/a.go (offset: 10)\n",
		},
		{
			name: "limit only", input: `{"file_path":"/tmp/a.go","limit":50}`,
			want: theme.IconFile + " Reading /tmp/a.go (limit: 50)\n",
		},
		{
			name: "offset and limit", input: `{"file_path":"/tmp/a.go","offset":10,"limit":50}`,
			want: theme.IconFile + " Reading /tmp/a.go (offset: 10, limit: 50)\n",
		},
		{
			name: "zero offset and limit are omitted", input: `{"file_path":"/tmp/a.go","offset":0,"limit":0}`,
			want: theme.IconFile + " Reading /tmp/a.go\n",
		},
		{
			name: "negative offset and limit are omitted", input: `{"file_path":"/tmp/a.go","offset":-5,"limit":-1}`,
			want: theme.IconFile + " Reading /tmp/a.go\n",
		},
		{
			// A missing file_path is not an error: the path renders empty.
			name: "missing file_path", input: `{"offset":3}`,
			want: theme.IconFile + " Reading  (offset: 3)\n",
		},
		{
			name: "empty object", input: `{}`,
			want: theme.IconFile + " Reading \n",
		},
		{
			// json null unmarshals cleanly into the struct, leaving zero values.
			name: "json null", input: `null`,
			want: theme.IconFile + " Reading \n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, lvl := range detailLevels {
				got := plain(FormatReadTool(json.RawMessage(tc.input), lvl))
				if got != tc.want {
					t.Errorf("detailLevel %q\n got: %q\nwant: %q", lvl, got, tc.want)
				}
			}
		})
	}
}

// TestFormatReadToolMalformed pins the empty-string degradation path.
func TestFormatReadToolMalformed(t *testing.T) {
	for _, input := range []string{
		`{"file_path":`, `not json`, ``, `[]`, `"str"`,
		`{"file_path":"/tmp/a.go","offset":"ten"}`, // wrong type for offset
	} {
		t.Run(input, func(t *testing.T) {
			if got := FormatReadTool(json.RawMessage(input), "full"); got != "" {
				t.Errorf("want empty string, got %q", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatTodoWriteTool
// ---------------------------------------------------------------------------

// TestFormatTodoWriteToolChecklist pins the status -> checkbox mapping over a
// fixture covering completed / in_progress / pending / an unknown status.
func TestFormatTodoWriteToolChecklist(t *testing.T) {
	want := theme.IconChecklist + " TODO List Updated:\n" +
		"  [*] Read the parser\n" +
		"  [→] Write the tests\n" +
		"  [ ] Run the gate\n" +
		"  [ ] Report back\n" // unknown statuses fall back to the unchecked box

	for _, lvl := range detailLevels {
		got := plain(FormatTodoWriteTool(fixture(t, "todos.json"), lvl))
		if got != want {
			t.Errorf("detailLevel %q\n got: %q\nwant: %q", lvl, got, want)
		}
	}
}

// TestFormatTodoWriteToolEdgeCases pins that structurally-valid-but-empty
// payloads still render the header (they are NOT treated as malformed), and
// that activeForm is never displayed.
func TestFormatTodoWriteToolEdgeCases(t *testing.T) {
	headerOnly := theme.IconChecklist + " TODO List Updated:\n"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty todo list", `{"todos":[]}`, headerOnly},
		{"missing todos key", `{}`, headerOnly},
		{"todos is json null", `{"todos":null}`, headerOnly},
		{"json null payload", `null`, headerOnly},
		{
			"activeForm is ignored",
			`{"todos":[{"content":"Do it","status":"in_progress","activeForm":"Doing it"}]}`,
			headerOnly + "  [→] Do it\n",
		},
		{
			"missing status defaults to unchecked",
			`{"todos":[{"content":"No status"}]}`,
			headerOnly + "  [ ] No status\n",
		},
		{
			"missing content renders an empty item",
			`{"todos":[{"status":"completed"}]}`,
			headerOnly + "  [*] \n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plain(FormatTodoWriteTool(json.RawMessage(tc.input), "full"))
			if got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestFormatTodoWriteToolMalformed pins the empty-string degradation path.
func TestFormatTodoWriteToolMalformed(t *testing.T) {
	for _, input := range []string{
		`{"todos":[`, `not json`, ``, `[]`, `"str"`,
		`{"todos":"nope"}`,           // wrong type for todos
		`{"todos":[{"status":123}]}`, // wrong type inside an item
	} {
		t.Run(input, func(t *testing.T) {
			if got := FormatTodoWriteTool(json.RawMessage(input), "full"); got != "" {
				t.Errorf("want empty string, got %q", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MakeWriteFormatter
// ---------------------------------------------------------------------------

// TestMakeWriteFormatter pins that the returned ToolFormatter is exactly
// FormatWriteTool with maxLines bound, across the truncation boundary and every
// detailLevel — and that each call returns an independently-bound closure.
func TestMakeWriteFormatter(t *testing.T) {
	inputs := []json.RawMessage{
		json.RawMessage(`{"file_path":"/tmp/a.go","old_string":"o1\no2\no3\no4","new_string":"n1\nn2\nn3"}`),
		fixture(t, "write_large.json"),
		json.RawMessage(`{"garbage":`),
		json.RawMessage(`{}`),
	}

	for _, maxLines := range []int{0, 1, 2, 4, 99} {
		f := MakeWriteFormatter(maxLines)
		if f == nil {
			t.Fatalf("MakeWriteFormatter(%d) returned nil", maxLines)
		}
		for _, input := range inputs {
			for _, lvl := range detailLevels {
				got := f(input, lvl)
				want := FormatWriteTool(input, maxLines, lvl)
				if got != want {
					t.Errorf("MakeWriteFormatter(%d)(_, %q)\n got: %q\nwant: %q", maxLines, lvl, got, want)
				}
			}
		}
	}

	// Two formatters with different bounds must not share state.
	input := json.RawMessage(`{"file_path":"/tmp/a.go","old_string":"o1\no2\no3","new_string":"n1"}`)
	tight := plain(MakeWriteFormatter(1)(input, "full"))
	loose := plain(MakeWriteFormatter(0)(input, "full"))
	if tight == loose {
		t.Fatalf("maxLines=1 and maxLines=0 produced identical output: %q", tight)
	}
	if !strings.Contains(tight, "... (2 more lines removed)") {
		t.Errorf("tight formatter did not truncate: %q", tight)
	}
	if strings.Contains(loose, "more lines removed") {
		t.Errorf("loose formatter unexpectedly truncated: %q", loose)
	}
}

// TestMakeWriteFormatterSatisfiesToolFormatter is a compile-time-ish assertion
// that the returned value is usable wherever a ToolFormatter is expected.
func TestMakeWriteFormatterSatisfiesToolFormatter(t *testing.T) {
	var f ToolFormatter = MakeWriteFormatter(3)
	if out := f(json.RawMessage(`{"file_path":"/tmp/a","content":"x"}`), "full"); out == "" {
		t.Error("expected non-empty output through the ToolFormatter interface")
	}
}
