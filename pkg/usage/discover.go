package usage

import (
	"os"
	"path/filepath"
	"strings"
)

// discoveredFile is a transcript file paired with the session/project it rolls
// up to. role is informational ("parent", "agent", "workflow").
type discoveredFile struct {
	Path        string
	SessionID   string
	ProjectPath string
	Role        string
}

// extractSessionParts derives the (sessionID, projectPath) ccusage attributes
// from a transcript file path, matching ccusage extract_session_parts:
//   - <slug>/<file>.jsonl            -> (file-without-ext, slug)
//     so each root agent-*.jsonl is its own "agent-<id>" session.
//   - <slug>/<sid>/subagents/.../x   -> (<sid>, slug...) workflow agents roll
//     up to the session-id directory component.
func extractSessionParts(path string) (string, string) {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	// Find the segment after "projects".
	projectsIdx := -1
	for i, p := range parts {
		if p == "projects" {
			projectsIdx = i
			break
		}
	}
	var rel []string
	if projectsIdx >= 0 && projectsIdx+1 < len(parts) {
		rel = parts[projectsIdx+1:]
	} else {
		rel = parts
	}

	fileSessionID := ""
	if len(rel) > 0 {
		last := rel[len(rel)-1]
		if strings.HasSuffix(last, ".jsonl") {
			fileSessionID = strings.TrimSuffix(last, ".jsonl")
		}
	}

	if len(rel) == 2 && fileSessionID != "" {
		return fileSessionID, rel[0]
	}
	if len(rel) >= 4 && rel[len(rel)-2] == "subagents" {
		sessionID := rel[len(rel)-3]
		project := strings.Join(rel[:len(rel)-3], string(filepath.Separator))
		if project == "" {
			project = "Unknown Project"
		}
		return sessionID, project
	}
	sessionID := "unknown"
	if len(rel) >= 2 {
		sessionID = rel[len(rel)-2]
	}
	project := "Unknown Project"
	if len(rel) > 2 {
		project = strings.Join(rel[:len(rel)-2], string(filepath.Separator))
	}
	return sessionID, project
}

// claudeProjectsDir returns the Claude projects directory. It honors
// CLAUDE_CONFIG_DIR (pointing either at a config dir that contains projects/, or
// directly at the projects/ dir) the same way ccusage does, falling back to
// ~/.claude/projects. The override lets the acceptance-gate script point both
// tools at a frozen snapshot of the live directory.
func claudeProjectsDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		// ccusage allows a path-list; only the first entry is needed here.
		if i := strings.IndexByte(dir, os.PathListSeparator); i >= 0 {
			dir = dir[:i]
		}
		candidate := filepath.Join(dir, "projects")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, nil
		}
		// CLAUDE_CONFIG_DIR may itself be the projects/ directory.
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// discoverSessionFiles collects every transcript file that belongs to the given
// session id, across the provided slug directories:
//   - the parent <sessionID>.jsonl;
//   - sibling root agent-*.jsonl whose inner sessionId == sessionID (the ad-hoc
//     Task subagents that discovery.go intentionally skips elsewhere);
//   - workflow agent-*.jsonl under <sessionID>/subagents/workflows/wf_*/.
//
// slugDirs are absolute paths to project-slug directories (a session can be
// fragmented across several). When empty, every slug under ~/.claude/projects
// is scanned.
func discoverSessionFiles(slugDirs []string, sessionID string) ([]discoveredFile, error) {
	if len(slugDirs) == 0 {
		root, err := claudeProjectsDir()
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				slugDirs = append(slugDirs, filepath.Join(root, e.Name()))
			}
		}
	}

	var files []discoveredFile
	for _, slugDir := range slugDirs {
		// Parent transcript.
		parent := filepath.Join(slugDir, sessionID+".jsonl")
		if fi, err := os.Stat(parent); err == nil && !fi.IsDir() {
			files = append(files, discoveredFile{Path: parent, SessionID: sessionID, Role: "parent"})
		}

		// Root ad-hoc agent-*.jsonl matched by inner sessionId.
		dirEntries, err := os.ReadDir(slugDir)
		if err != nil {
			continue
		}
		for _, de := range dirEntries {
			name := de.Name()
			if de.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			p := filepath.Join(slugDir, name)
			if innerSessionID(p) == sessionID {
				files = append(files, discoveredFile{Path: p, SessionID: sessionID, Role: "agent"})
			}
		}

		// Workflow agent files under <sessionID>/subagents/workflows/wf_*/.
		workflowsDir := filepath.Join(slugDir, sessionID, "subagents", "workflows")
		files = append(files, discoverWorkflowAgentFiles(workflowsDir, sessionID)...)
	}
	return files, nil
}

// discoverWorkflowAgentFiles returns the agent-*.jsonl transcripts under each
// wf_* run directory of workflowsDir. The token-less journal.jsonl is ignored.
// A missing directory is normal (no workflow ran) and yields nothing.
func discoverWorkflowAgentFiles(workflowsDir, sessionID string) []discoveredFile {
	runs, err := os.ReadDir(workflowsDir)
	if err != nil {
		return nil
	}
	var files []discoveredFile
	for _, run := range runs {
		if !run.IsDir() || !strings.HasPrefix(run.Name(), "wf_") {
			continue
		}
		runDir := filepath.Join(workflowsDir, run.Name())
		runFiles, err := os.ReadDir(runDir)
		if err != nil {
			continue
		}
		for _, f := range runFiles {
			name := f.Name()
			if f.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			files = append(files, discoveredFile{
				Path:      filepath.Join(runDir, name),
				SessionID: sessionID,
				Role:      "workflow",
			})
		}
	}
	return files
}
