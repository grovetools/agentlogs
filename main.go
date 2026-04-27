package main

import (
	"os"

	grovelogging "github.com/grovetools/core/logging"

	"github.com/grovetools/agentlogs/cmd"
)

func main() {
	// CLI output goes to stdout (stderr is for errors only)
	grovelogging.SetGlobalOutput(os.Stdout)

	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
