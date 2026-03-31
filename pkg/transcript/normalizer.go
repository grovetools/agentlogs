package transcript

// Normalizer converts provider-specific transcript formats to UnifiedEntry.
type Normalizer interface {
	// NormalizeLine normalizes a single JSON line (for JSONL formats).
	NormalizeLine(line []byte) (*UnifiedEntry, error)

	// Provider returns the provider name.
	Provider() string
}
