package main

import (
	"os"

	"github.com/mattsolo1/grove-agent-logs/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
