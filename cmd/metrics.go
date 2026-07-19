package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	"github.com/grovetools/agentlogs/internal/provider"
	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/pkg/metrics"
)

func newMetricsCmd() *cobra.Command {
	var jsonOutput bool
	var showFiles bool

	cmd := cli.NewStandardCommand("metrics", "Compute process metrics for a session")
	cmd.Use = "metrics <spec>"
	cmd.Long = `Computes deterministic process metrics for a session transcript.

<spec> can be a plan/job, a session ID, or a direct path to a log file.

Reports tool calls, distinct tools, turns, and (where the provider supports it)
the number of files touched and edited. Counts are folded from the normalized
transcript and exclude sidechain (subagent) entries.

File counts are omitted entirely for providers whose tool vocabulary does not
expose structured file paths, rather than being reported as zero; such
providers list the missing measurements under "unsupported".

Token counts and wall-clock time are reported under "diagnostics" and are
cross-checks only, not evaluation axes.

This command always reads transcripts from disk. Its output does not depend on
whether the grove daemon is running.`
	cmd.Args = cobra.ExactArgs(1)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		spec := args[0]

		sessionInfo, err := resolveMetricsSession(spec)
		if err != nil {
			return err
		}

		// Deliberately nil daemon client: SelectSource guards its entire
		// daemon branch on `daemonClient != nil` (internal/provider/router.go),
		// so passing nil degrades cleanly to the per-provider file sources
		// while still getting the normalizer Flush() those sources perform.
		src := provider.SelectSource(sessionInfo, nil)

		entries, err := src.Read(context.Background(), sessionInfo, provider.ReadOptions{
			DetailLevel: "full",
			EndLine:     -1,
		})
		if err != nil {
			return fmt.Errorf("error reading transcript: %w", err)
		}

		result := metrics.Compute(entries)
		result.SessionID = sessionInfo.SessionID
		if result.Provider == "" {
			// Empty transcripts declare no provider; fall back to the resolved
			// session so the output still identifies what was measured.
			result.Provider = sessionInfo.Provider
		}
		if !showFiles {
			result.TouchedFiles = nil
			result.EditedFiles = nil
		}

		if jsonOutput {
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal metrics: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		printMetrics(result, showFiles)
		return nil
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&showFiles, "files", false, "Include the touched/edited file lists")

	return cmd
}

// resolveMetricsSession mirrors the two-branch spec resolution in tokens.go: a
// direct file path is used as-is with the provider inferred from the path,
// otherwise the spec is resolved as a plan/job/session ID.
func resolveMetricsSession(spec string) (*session.SessionInfo, error) {
	fileInfo, statErr := os.Stat(spec)
	if statErr != nil || fileInfo.IsDir() {
		info, err := session.ResolveSessionInfo(spec)
		if err != nil {
			return nil, fmt.Errorf("could not resolve session for '%s': %w", spec, err)
		}
		return info, nil
	}

	prov := "claude"
	if strings.Contains(spec, "/.codex/") || strings.Contains(spec, "/codex/sessions/") {
		prov = "codex"
	} else if strings.Contains(spec, "/opencode/storage/") {
		prov = "opencode"
	} else if strings.Contains(spec, "/pi/sessions/") {
		prov = "pi"
	}

	sessionID := "unknown"
	if prov == "opencode" {
		sessionID = strings.TrimSuffix(filepath.Base(spec), ".json")
	}
	pathParts := strings.Split(spec, "/")
	for i, part := range pathParts {
		if part == ".claude" || part == ".codex" {
			if i+1 < len(pathParts) {
				sessionID = pathParts[i+1]
			}
			break
		}
	}

	return &session.SessionInfo{
		LogFilePath: spec,
		Provider:    prov,
		SessionID:   sessionID,
	}, nil
}

// printOptionalInt renders a pointer count, distinguishing an unmeasured nil
// from a measured zero (D4). label carries its own padding.
func printOptionalInt(label string, v *int) {
	if v == nil {
		fmt.Printf("%s not measured\n", label)
		return
	}
	fmt.Printf("%s %d\n", label, *v)
}

func printMetrics(result metrics.Result, showFiles bool) {
	fmt.Printf("Process Metrics for Session: %s\n", result.SessionID)
	fmt.Printf("Provider: %s\n", result.Provider)
	fmt.Println(strings.Repeat("─", 50))
	printOptionalInt("Tool calls:             ", result.ToolCalls)
	printOptionalInt("Distinct tools:         ", result.DistinctTools)
	printOptionalInt("Turns:                  ", result.Turns)

	if result.FilesTouched != nil {
		fmt.Printf("Files touched:           %d\n", *result.FilesTouched)
	} else {
		fmt.Printf("Files touched:           not measured\n")
	}
	if result.FilesEdited != nil {
		fmt.Printf("Files edited:            %d\n", *result.FilesEdited)
	} else {
		fmt.Printf("Files edited:            not measured\n")
	}

	if len(result.Unsupported) > 0 {
		fmt.Printf("\nUnsupported for provider %q: %s\n",
			result.Provider, strings.Join(result.Unsupported, ", "))
	}

	if showFiles {
		if len(result.TouchedFiles) > 0 {
			fmt.Println("\nTouched files:")
			for _, f := range result.TouchedFiles {
				fmt.Printf("  %s\n", f)
			}
		}
		if len(result.EditedFiles) > 0 {
			fmt.Println("\nEdited files:")
			for _, f := range result.EditedFiles {
				fmt.Printf("  %s\n", f)
			}
		}
	}

	fmt.Println("\nDiagnostics (cross-check only, not evaluation axes):")
	if result.Diagnostics.WallClockSeconds != nil {
		fmt.Printf("  Wall clock:            %.1fs\n", *result.Diagnostics.WallClockSeconds)
	} else {
		fmt.Printf("  Wall clock:            not measured\n")
	}
	fmt.Printf("  Input tokens:          %d\n", result.Diagnostics.Tokens.Input)
	fmt.Printf("  Output tokens:         %d\n", result.Diagnostics.Tokens.Output)
	fmt.Printf("  Cache read:            %d\n", result.Diagnostics.Tokens.CacheRead)
	fmt.Printf("  Cache write:           %d\n", result.Diagnostics.Tokens.CacheWrite)
}
