package session

import (
	"os"
	"path/filepath"
	"testing"
)

// setupPointerFixture builds a hooks session registry entry (as written by
// the grove opencode plugin v2) plus a matching opencode fragment store, and
// returns the expected session info path.
func setupPointerFixture(t *testing.T) string {
	t.Helper()

	stateHome := t.TempDir()
	t.Setenv("GROVE_HOME", "")
	t.Setenv("XDG_STATE_HOME", stateHome)

	storageRoot := filepath.Join(t.TempDir(), "opencode", "storage")
	sessionInfoDir := filepath.Join(storageRoot, "session", "proj_abc")
	if err := os.MkdirAll(sessionInfoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionInfoPath := filepath.Join(sessionInfoDir, "ses_ptr001.json")
	if err := os.WriteFile(sessionInfoPath, []byte(`{"id":"ses_ptr001","projectID":"proj_abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	registryDir := filepath.Join(stateHome, "grove", "hooks", "sessions", "ses_ptr001")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `{
  "session_id": "flow-job-7",
  "claude_session_id": "ses_ptr001",
  "provider": "opencode",
  "native_session_id": "ses_ptr001",
  "opencode_storage_root": ` + jsonString(storageRoot) + `,
  "working_directory": "/tmp/ptr-project",
  "plan_name": "my-plan",
  "job_file_path": "/plans/my-plan/03-impl.md",
  "pid": 4242,
  "started_at": "2026-07-01T12:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(registryDir, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	return sessionInfoPath
}

func jsonString(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '"' {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return string(append(b, '"'))
}

func TestResolveOpenCodePointer(t *testing.T) {
	sessionInfoPath := setupPointerFixture(t)

	specs := map[string]string{
		"flow job id": "flow-job-7",
		"native id":   "ses_ptr001",
		"plan/job":    "my-plan/03-impl.md",
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			info := resolveOpenCodePointer(spec)
			if info == nil {
				t.Fatalf("resolveOpenCodePointer(%q) = nil", spec)
			}
			if info.SessionID != "ses_ptr001" {
				t.Errorf("SessionID = %q, want ses_ptr001", info.SessionID)
			}
			if info.Provider != "opencode" {
				t.Errorf("Provider = %q, want opencode", info.Provider)
			}
			if info.LogFilePath != sessionInfoPath {
				t.Errorf("LogFilePath = %q, want %q", info.LogFilePath, sessionInfoPath)
			}
			if len(info.Jobs) != 1 || info.Jobs[0].Plan != "my-plan" || info.Jobs[0].Job != "03-impl.md" {
				t.Errorf("Jobs = %+v", info.Jobs)
			}
			if info.PID != 4242 {
				t.Errorf("PID = %d, want 4242", info.PID)
			}
		})
	}
}

func TestResolveOpenCodePointerNoMatch(t *testing.T) {
	setupPointerFixture(t)

	if info := resolveOpenCodePointer("some-other-session"); info != nil {
		t.Errorf("expected nil for unmatched spec, got %+v", info)
	}
	if info := resolveOpenCodePointer(""); info != nil {
		t.Errorf("expected nil for empty spec, got %+v", info)
	}
}

func TestResolveOpenCodePointerIgnoresOtherProviders(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("GROVE_HOME", "")
	t.Setenv("XDG_STATE_HOME", stateHome)

	registryDir := filepath.Join(stateHome, "grove", "hooks", "sessions", "claude-uuid-1")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `{"session_id": "claude-job", "claude_session_id": "claude-uuid-1", "provider": "claude"}`
	if err := os.WriteFile(filepath.Join(registryDir, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	if info := resolveOpenCodePointer("claude-job"); info != nil {
		t.Errorf("claude sessions must not resolve through the opencode pointer, got %+v", info)
	}
}

func TestOpenCodeSessionInfoPathMissing(t *testing.T) {
	if p := openCodeSessionInfoPath(t.TempDir(), "ses_none"); p != "" {
		t.Errorf("expected empty path, got %q", p)
	}
	if p := openCodeSessionInfoPath(t.TempDir(), ""); p != "" {
		t.Errorf("expected empty path for empty id, got %q", p)
	}
}
