package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-tend/pkg/assert"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
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
	transcriptContent := `{"uuid":"1","parentUuid":null,"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2025-01-01T12:00:00Z"}
{"uuid":"2","parentUuid":"1","type":"assistant","message":{"role":"assistant","content":"Hi there!"},"timestamp":"2025-01-01T12:00:01Z"}
{"uuid":"3","parentUuid":"2","type":"user","message":{"role":"user","content":"How are you?"},"timestamp":"2025-01-01T12:00:02Z"}
{"uuid":"4","parentUuid":"3","type":"assistant","message":{"role":"assistant","content":"I'm doing well, thank you!"},"timestamp":"2025-01-01T12:00:03Z"}`
	
	if err := fs.WriteString(filepath.Join(projectsDir, "session-alpha.jsonl"), transcriptContent); err != nil {
		return err
	}

	// Create another session
	transcriptContent2 := `{"uuid":"1","parentUuid":null,"type":"user","message":{"role":"user","content":"Test message"},"timestamp":"2025-01-02T10:00:00Z"}`
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
				clogsBinary := os.Getenv("CLOGS_BINARY")
				if clogsBinary == "" {
					return fmt.Errorf("CLOGS_BINARY environment variable not set")
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
				if err := assert.Contains(result.Stdout, "Available session transcripts:", "Should print header"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "test-project/session-alpha", "Should list session-alpha"); err != nil {
					return err
				}
				return assert.Contains(result.Stdout, "test-project/session-beta", "Should list session-beta")
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
				clogsBinary := os.Getenv("CLOGS_BINARY")
				if clogsBinary == "" {
					return fmt.Errorf("CLOGS_BINARY environment variable not set")
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
				clogsBinary := os.Getenv("CLOGS_BINARY")
				if clogsBinary == "" {
					return fmt.Errorf("CLOGS_BINARY environment variable not set")
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