package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
				
				if err := assert.Equal(0, result.ExitCode, "clogs list should exit successfully"); err != nil {
					return err
				}
				
				// For now, just check that it prints the placeholder message
				// Note: cobra's cmd.Println writes to stderr by default
				output := result.Stdout + result.Stderr
				return assert.Contains(output, "Listing available session transcripts", "Should print list message")
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
				
				// For now, just check that it prints the placeholder message with session ID
				// Note: cobra's cmd.Printf writes to stderr by default
				output := result.Stdout + result.Stderr
				return assert.Contains(output, "Tailing transcript for session: session-alpha", "Should print tail message with session ID")
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
				
				// Check that it prints the placeholder message with session ID and role
				// Note: cobra's cmd.Printf writes to stderr by default
				output := result.Stdout + result.Stderr
				if !strings.Contains(output, "Querying transcript for session: session-alpha") {
					return fmt.Errorf("output should contain session ID")
				}
				return assert.Contains(output, "role: user", "Should print role filter")
			}),
		},
	}
}