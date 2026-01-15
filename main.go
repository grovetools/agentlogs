package main

import (
	"os"

	"github.com/mattsolo1/grove-agent-logs/cmd"
	grovelogging "github.com/mattsolo1/grove-core/logging"
)

func main() {
	// CLI output goes to stdout (stderr is for errors only)
	grovelogging.SetGlobalOutput(os.Stdout)

	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
