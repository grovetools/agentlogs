package claudelogs

import (
	"github.com/mattsolo1/grove-claude-logs/internal/transcript"
)

// Parser wraps the internal transcript parser
type Parser struct {
	*transcript.Parser
}

// NewParser creates a new transcript parser
func NewParser() *Parser {
	return &Parser{
		Parser: transcript.NewParser(),
	}
}

// ParseFile parses a Claude transcript file and returns extracted messages
func (p *Parser) ParseFile(path string) ([]transcript.ExtractedMessage, error) {
	return p.Parser.ParseFile(path)
}

// ParseFileFromOffset parses a file starting from a specific offset
func (p *Parser) ParseFileFromOffset(path string, offset int64) ([]transcript.ExtractedMessage, int64, error) {
	return p.Parser.ParseFileFromOffset(path, offset)
}

// GetTranscriptPath returns the path to a transcript file for a given session ID
func GetTranscriptPath(sessionID string) (string, error) {
	return transcript.GetTranscriptPath(sessionID)
}