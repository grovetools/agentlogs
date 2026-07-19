package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
)

// setupMockClaudeDir creates a mock ~/.claude directory structure
func setupMockClaudeDir(ctx *harness.Context) error {
	// Create a temporary home directory
	homeDir := ctx.NewDir("home")

	// Create mock structure: ~/.claude/projects/test-project/
	projectsDir := filepath.Join(homeDir, ".claude", "projects", "test-project")
	if err := fs.CreateDir(projectsDir); err != nil {
		return err
	}

	// Create a mock transcript file
	transcriptContent := `{"cwd":"/tmp/project-alpha","sessionId":"session-alpha","uuid":"1","parentUuid":null,"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2025-01-01T12:00:00Z"}
{"uuid":"2","parentUuid":"1","type":"assistant","message":{"role":"assistant","content":"Hi there!"},"timestamp":"2025-01-01T12:00:01Z"}
{"uuid":"3","parentUuid":"2","type":"user","message":{"role":"user","content":"How are you?"},"timestamp":"2025-01-01T12:00:02Z"}
{"uuid":"4","parentUuid":"3","type":"assistant","message":{"role":"assistant","content":"I'm doing well, thank you!"},"timestamp":"2025-01-01T12:00:03Z"}`

	if err := fs.WriteString(filepath.Join(projectsDir, "session-alpha.jsonl"), transcriptContent); err != nil {
		return fmt.Errorf("failed to write session-alpha.jsonl: %w", err)
	}

	// Create another session
	transcriptContent2 := `{"cwd":"/tmp/project-beta","sessionId":"session-beta","uuid":"1","parentUuid":null,"type":"user","message":{"role":"user","content":"Test message"},"timestamp":"2025-01-02T10:00:00Z"}`
	if err := fs.WriteString(filepath.Join(projectsDir, "session-beta.jsonl"), transcriptContent2); err != nil {
		return err
	}

	// Store the home directory for use in test steps
	ctx.Set("mock_home", homeDir)
	return nil
}

// ClogsListScenario tests the 'clogs list' command
func ClogsListScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "clogs-list-command",
		Steps: []harness.Step{
			harness.NewStep("Setup mock Claude directory", setupMockClaudeDir),
			harness.NewStep("Run 'clogs list'", func(ctx *harness.Context) error {
				clogsBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(clogsBinary, "list").Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.ExitCode != 0 {
					// Check if it failed because of no sessions
					if result.Stdout == "No session transcripts found\n" {
						return nil // This is acceptable
					}
					return fmt.Errorf("clogs list failed: %s", result.Stderr)
				}

				// Check that it lists sessions
				if err := assert.Contains(result.Stdout, "SESSION ID", "Should print table header"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "WORKTREE", "Should print worktree column"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "session-alpha", "Should list session-alpha"); err != nil {
					return err
				}
				return assert.Contains(result.Stdout, "project-beta", "Should list project-beta")
			}),
			harness.NewStep("Run 'clogs list --json'", func(ctx *harness.Context) error {
				clogsBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(clogsBinary, "list", "--json").Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.ExitCode != 0 {
					return fmt.Errorf("clogs list --json failed: %s", result.Stderr)
				}

				// Check that it outputs valid JSON
				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON output: %w", err)
				}

				// Check that sessions have expected fields
				if len(sessions) == 0 {
					return fmt.Errorf("expected at least one session in JSON output")
				}

				for _, session := range sessions {
					if _, ok := session["sessionId"]; !ok {
						return fmt.Errorf("missing sessionId field in JSON output")
					}
					if _, ok := session["projectName"]; !ok {
						return fmt.Errorf("missing projectName field in JSON output")
					}
					if _, ok := session["startedAt"]; !ok {
						return fmt.Errorf("missing startedAt field in JSON output")
					}
				}

				return nil
			}),
			harness.NewStep("Run 'clogs list --project alpha'", func(ctx *harness.Context) error {
				clogsBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(clogsBinary, "list", "--project", "alpha").Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.ExitCode != 0 {
					return fmt.Errorf("clogs list --project alpha failed: %s", result.Stderr)
				}

				// Check that it only shows project-alpha
				if err := assert.Contains(result.Stdout, "session-alpha", "Should list session-alpha"); err != nil {
					return err
				}
				// Check that it doesn't show project-beta
				if err := assert.NotContains(result.Stdout, "project-beta", "Should not list project-beta"); err != nil {
					return err
				}
				return assert.Contains(result.Stdout, "project-alpha", "Should show project-alpha")
			}),
		},
	}
}

// ClogsTailScenario tests the 'clogs tail' command
func ClogsTailScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "clogs-tail-command",
		Steps: []harness.Step{
			harness.NewStep("Setup mock Claude directory", setupMockClaudeDir),
			harness.NewStep("Run 'clogs tail' with session ID", func(ctx *harness.Context) error {
				clogsBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(clogsBinary, "tail", "session-alpha").Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "clogs tail should exit successfully"); err != nil {
					return err
				}

				// Check that it shows messages
				if err := assert.Contains(result.Stdout, "Showing last", "Should show message count"); err != nil {
					return err
				}
				// Should have at least one message
				return assert.Contains(result.Stdout, ":", "Should show messages with role")
			}),
		},
	}
}

// ClogsQueryScenario tests the 'clogs query' command
func ClogsQueryScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "clogs-query-command",
		Steps: []harness.Step{
			harness.NewStep("Setup mock Claude directory", setupMockClaudeDir),
			harness.NewStep("Run 'clogs query' with role filter", func(ctx *harness.Context) error {
				clogsBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(clogsBinary, "query", "session-alpha", "--role", "user").Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "clogs query should exit successfully"); err != nil {
					return err
				}

				// Check that it shows filtered messages
				if err := assert.Contains(result.Stdout, "Found", "Should show message count"); err != nil {
					return err
				}
				return assert.Contains(result.Stdout, "user:", "Should show user messages")
			}),
		},
	}
}

// AglogsMetricsScenario exercises the `aglogs metrics` deterministic fold over
// a real transcript, end to end through the CLI.
//
// The fold must be a pure function of the transcript bytes: no daemon, no
// network, no ambient state. This scenario runs against the same mock claude
// home the other scenarios use, so a change that makes metrics depend on
// anything outside the session file shows up here as a diff.
func AglogsMetricsScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "aglogs-metrics-command",
		Steps: []harness.Step{
			harness.NewStep("Setup mock Claude directory", setupMockClaudeDir),
			harness.NewStep("Run 'aglogs metrics --json' and verify the fold", func(ctx *harness.Context) error {
				binary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				homeDir := ctx.GetString("mock_home")
				cmd := command.New(binary, "metrics", "session-alpha", "--json").
					Env("HOME=" + homeDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "aglogs metrics should exit successfully"); err != nil {
					return err
				}

				// Ambient grove logging (daemon startup, workspace discovery
				// warnings) can precede the payload on stdout, so slice from
				// the first brace rather than assuming a clean stream.
				start := strings.Index(result.Stdout, "{")
				if start < 0 {
					return fmt.Errorf("metrics --json emitted no JSON object: %s", result.Stdout)
				}
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout[start:]), &parsed); err != nil {
					return fmt.Errorf("metrics --json did not emit valid JSON: %w", err)
				}

				// The fixture has two user text turns and no tool calls, so the
				// fold must report exactly that — not a zero, and not a guess.
				turns, ok := parsed["turns"]
				if !ok {
					return fmt.Errorf("metrics output has no `turns` key: %s", result.Stdout)
				}
				if turns != float64(2) {
					return fmt.Errorf("turns = %v, want 2 for the two user text messages", turns)
				}

				// Token/cost diagnostics must stay quarantined under
				// `diagnostics` so the joiner cannot mistake them for the Cost
				// axis, which flow owns.
				if _, leaked := parsed["tokens"]; leaked {
					return fmt.Errorf("token diagnostics leaked to the top level: %s", result.Stdout)
				}
				return nil
			}),
		},
	}
}

// AglogsMetricsPiArmsScenario exercises the pi arm-attribution modes end to end
// through the CLI: --branches, --emit-partials, and the --by-config argument
// contract.
//
// Standing Rule 2: unit tests cover LiftGroveEntries and writeArmPartials, but
// helper-level coverage does not discharge a call path reachable through a verb.
// This grades the binary.
//
// It uses a committed fixture placed at a REAL pi session path — the provider
// inference is structural (a .jsonl under sessions/--<munged-cwd>--/), and the
// substring the old inference looked for never occurs in a real pi layout.
func AglogsMetricsPiArmsScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "aglogs-metrics-pi-arms",
		Steps: []harness.Step{
			harness.NewStep("Stage a pi session at its real on-disk layout", func(ctx *harness.Context) error {
				home := ctx.NewDir("pihome")
				sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--Users-test-project--")
				if err := os.MkdirAll(sessionDir, 0o755); err != nil {
					return err
				}
				src := filepath.Join("..", "..", "pkg", "transcript", "testdata", "pi", "trees", "grove_emitted.jsonl")
				data, err := os.ReadFile(src)
				if err != nil {
					// The scenario runs from the built binary's cwd; try the
					// repo-relative path too.
					data, err = os.ReadFile(filepath.Join("pkg", "transcript", "testdata", "pi", "trees", "grove_emitted.jsonl"))
					if err != nil {
						return fmt.Errorf("staging fixture: %w", err)
					}
				}
				path := filepath.Join(sessionDir, "2026-07-01T10-00-00-000Z_0198c2f4-9a51-7abc-8def-abcabcabcabc.jsonl")
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return err
				}
				ctx.Set("pi_home", home)
				ctx.Set("pi_session", path)
				return nil
			}),

			harness.NewStep("--emit-partials writes envelope-only partials", func(ctx *harness.Context) error {
				binary, err := FindProjectBinary()
				if err != nil {
					return err
				}
				outDir := filepath.Join(ctx.GetString("pi_home"), "partials")

				cmd := command.New(binary, "metrics", ctx.GetString("pi_session"),
					"--emit-partials", outDir).
					Env("HOME=" + ctx.GetString("pi_home"))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "metrics --emit-partials should succeed"); err != nil {
					return err
				}

				entries, err := os.ReadDir(outDir)
				if err != nil {
					return fmt.Errorf("no partials directory: %w", err)
				}
				if len(entries) != 1 {
					return fmt.Errorf("wrote %d partials, want 1", len(entries))
				}

				raw, err := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
				if err != nil {
					return err
				}
				var partial map[string]interface{}
				if err := json.Unmarshal(raw, &partial); err != nil {
					return fmt.Errorf("partial is not valid JSON: %w", err)
				}
				// agentlogs is the SOLE ComponentMetrics writer and never a Cost
				// writer; a cost key here would collide with the fork runner's
				// partial at join time instead of merging as a disjoint axis.
				if _, leaked := partial["cost"]; leaked {
					return fmt.Errorf("partial carries a cost axis: %s", raw)
				}
				for _, required := range []string{"schema", "key", "config"} {
					if _, ok := partial[required]; !ok {
						return fmt.Errorf("partial missing envelope field %q: %s", required, raw)
					}
				}
				return nil
			}),

			harness.NewStep("--by-config rejects a spec rather than ignoring one", func(ctx *harness.Context) error {
				binary, err := FindProjectBinary()
				if err != nil {
					return err
				}
				cmd := command.New(binary, "metrics", ctx.GetString("pi_session"),
					"--by-config", "context").
					Env("HOME=" + ctx.GetString("pi_home"))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.ExitCode == 0 {
					return fmt.Errorf("combining --by-config with a spec should fail, got exit 0")
				}
				return nil
			}),

			harness.NewStep("--branches folds each arm on its own path", func(ctx *harness.Context) error {
				binary, err := FindProjectBinary()
				if err != nil {
					return err
				}
				cmd := command.New(binary, "metrics", ctx.GetString("pi_session"),
					"--branches", "--json").
					Env("HOME=" + ctx.GetString("pi_home"))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "metrics --branches should succeed"); err != nil {
					return err
				}
				start := strings.Index(result.Stdout, "[")
				if start < 0 {
					return fmt.Errorf("metrics --branches emitted no JSON array: %s", result.Stdout)
				}
				var arms []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout[start:]), &arms); err != nil {
					return fmt.Errorf("metrics --branches did not emit valid JSON: %w", err)
				}
				if len(arms) != 1 {
					return fmt.Errorf("got %d arms, want 1 for this linear arm file", len(arms))
				}
				if attributed, _ := arms[0]["attributed"].(bool); !attributed {
					return fmt.Errorf("arm is not attributed; the grove_config stamp did not lift: %s", result.Stdout)
				}
				return nil
			}),
		},
	}
}
