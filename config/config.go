package config

//go:generate go run ../tools/schema-generator

// TranscriptConfig defines settings for transcript viewing.
type TranscriptConfig struct {
	// DetailLevel controls the verbosity of transcript output.
	// "summary" (default): Shows a one-line summary of tool calls.
	// "full": Shows full tool inputs and outputs.
	DetailLevel string `yaml:"detail_level,omitempty" jsonschema:"description=Verbosity of transcript output: summary or full,enum=summary,enum=full,default=summary" jsonschema_extras:"x-layer=global,x-priority=60"`

	// MaxDiffLines controls how many lines of a diff to show before truncating.
	// 0 (default): Show all diff lines without truncation.
	// >0: Show at most this many lines, then summarize the rest.
	MaxDiffLines int `yaml:"max_diff_lines,omitempty" jsonschema:"description=Lines of diff to show before truncating (0=unlimited),default=0" jsonschema_extras:"x-layer=global,x-priority=61"`
}

// Config is the top-level configuration structure for aglogs.
type Config struct {
	Transcript TranscriptConfig `yaml:"transcript,omitempty" jsonschema:"description=Transcript viewing settings" jsonschema_extras:"x-layer=global,x-priority=60"`
}
