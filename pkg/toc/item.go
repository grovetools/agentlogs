// Package toc derives a table-of-contents outline from a provider-normalized
// agent transcript and renders it, styled or plain, for non-interactive
// consumers. It is the shared seam between treemux's interactive outline
// drawer (which overlays cursor, fold state and follow mode on top of the
// items built here) and headless consumers such as flow's job artifacts and
// status panes.
package toc

// Item is one outline row. Agent transcript rows are built by BuildAgentItems;
// editor and shell outlines (treemux-side) fold their own rows into the same
// shape so a single type flows through every renderer.
type Item struct {
	ID, Title string
	Kind      string
	// Tool is the raw tool-call name behind a Kind "tool" row (Bash, read,
	// grove_concepts, …). The rendered title is already summarized and
	// truncated, so the name has to travel separately for per-tool iconography.
	Tool    string
	Snippet string
	EntryID string
	Status  string
	Level   int
	// HeadingLevel preserves the source Markdown's H1-H6 level when Level also
	// includes the enclosing transcript response.
	HeadingLevel int
	// MarkdownLine is the one-based source line for a transcript Markdown
	// heading. Unlike Line, it is never interpreted as an editor jump target.
	MarkdownLine int
	// Markdown is the full assistant prose behind a transcript response row.
	// Renderers can show it inline without reaching back into provider data.
	Markdown         string
	Line             int
	Window           int
	Current          bool
	HasKids          bool
	Expanded         bool
	DescendantCount  int
	Buffer           int
	ChangedTick      int
	SourceGeneration uint64
	Actionable       bool
}
