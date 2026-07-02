package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/agentlogs/internal/opencode"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// opencodeUsageSource scans opencode's fragmented storage
// (~/.local/share/opencode/storage: session/ + message/ + part/) through the
// fragment assembler — opencode has no transcript files to walk.
type opencodeUsageSource struct{}

func (opencodeUsageSource) Provider() string { return "opencode" }

// CollectEntries loads per-message usage entries for every opencode session
// found under the default storage root. A missing store yields (nil, nil).
func (opencodeUsageSource) CollectEntries() ([]loadedEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	storageDir := filepath.Join(homeDir, ".local", "share", "opencode", "storage")
	if _, err := os.Stat(storageDir); err != nil {
		return nil, nil
	}
	return collectOpenCodeEntries(storageDir)
}

// collectOpenCodeEntries walks <storage>/session/<projectID>/ses_*.json and
// assembles each session's messages for their usage.
func collectOpenCodeEntries(storageDir string) ([]loadedEntry, error) {
	infoFiles, err := filepath.Glob(filepath.Join(storageDir, "session", "*", "ses*.json"))
	if err != nil || len(infoFiles) == 0 {
		return nil, nil
	}
	assembler, err := opencode.NewAssemblerWithDir(storageDir)
	if err != nil {
		return nil, nil
	}
	var all []loadedEntry
	for _, infoPath := range infoFiles {
		sessionID := strings.TrimSuffix(filepath.Base(infoPath), ".json")
		projectPath := opencodeSessionDirectory(infoPath)
		if projectPath == "" {
			projectPath = filepath.Base(filepath.Dir(infoPath)) // projectID
		}
		entries, err := opencodeSessionEntries(assembler, sessionID, projectPath)
		if err != nil {
			continue
		}
		all = append(all, entries...)
	}
	return all, nil
}

// opencodeSessionEntries converts one assembled opencode session into usage
// entries: every message carrying tokens, with opencode's own computed cost
// ingested as the native cost (EntryCost gives it precedence) and the model
// keyed "providerID/modelID" — the models.dev key shape — for the pricing
// fallback when a message reports zero cost. Message ids are globally unique
// (msg_*), so the shared dedup pass is a no-op for opencode.
func opencodeSessionEntries(assembler *opencode.Assembler, sessionID, projectPath string) ([]loadedEntry, error) {
	assembled, err := assembler.AssembleTranscript(sessionID)
	if err != nil {
		return nil, err
	}
	var entries []loadedEntry
	for i := range assembled {
		e := &assembled[i]
		if e.Tokens == nil {
			continue
		}
		model := e.ModelID
		if e.ProviderID != "" && e.ModelID != "" {
			model = e.ProviderID + "/" + e.ModelID
		}
		var costUSD *float64
		if e.CostUSD > 0 {
			c := e.CostUSD
			costUSD = &c
		}
		entries = append(entries, loadedEntry{
			SessionID:   sessionID,
			ProjectPath: projectPath,
			MessageID:   e.MessageID,
			Model:       model,
			Timestamp:   e.Timestamp,
			Usage: transcript.Usage{
				InputTokens:              e.Tokens.Input,
				OutputTokens:             e.Tokens.Output,
				CacheReadInputTokens:     e.Tokens.CacheRead,
				CacheCreationInputTokens: e.Tokens.CacheWrite,
			},
			Provider: "opencode",
			CostUSD:  costUSD,
		})
	}
	return entries, nil
}

// opencodeSessionDirectory reads the session info file's "directory" field
// (the session's working directory — the natural project path).
func opencodeSessionDirectory(infoPath string) string {
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return ""
	}
	var info struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.Directory
}
