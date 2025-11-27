package config

//go:generate go run ../tools/schema-generator

// TranscriptConfig defines settings for transcript viewing.
type TranscriptConfig struct {
	// DetailLevel controls the verbosity of transcript output.
	// "summary" (default): Shows a one-line summary of tool calls.
	// "full": Shows full tool inputs and outputs.
	DetailLevel string `yaml:"detail_level,omitempty"`

	// MaxDiffLines controls how many lines of a diff to show before truncating.
	// 0 (default): Show all diff lines without truncation.
	// >0: Show at most this many lines, then summarize the rest.
	MaxDiffLines int `yaml:"max_diff_lines,omitempty"`
}

// Config is the top-level configuration structure for aglogs.
type Config struct {
	Transcript TranscriptConfig `yaml:"transcript,omitempty"`
}
