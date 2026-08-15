package cmd

import (
	"context"
	"os"

	"github.com/grovetools/agentlogs/pkg/agentstream"
	"github.com/spf13/cobra"
)

func newSuperviseAgentCmd() *cobra.Command {
	var opts agentstream.SupervisorOptions
	cmd := &cobra.Command{
		Use:          "_supervise-agent",
		Short:        "Run an interactive provider under lifecycle supervision",
		Hidden:       true,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			os.Exit(runSupervisor(cmd.Context(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.PIDFile, "pid-file", "", "provider PID receipt path")
	cmd.Flags().StringVar(&opts.AgentCommand, "agent-command", "", "provider shell command")
	cmd.Flags().StringVar(&opts.ReporterCommand, "reporter-command", "", "reporter shell command prefix")
	_ = cmd.MarkFlagRequired("pid-file")
	_ = cmd.MarkFlagRequired("agent-command")
	_ = cmd.MarkFlagRequired("reporter-command")
	return cmd
}

var runSupervisor = func(ctx context.Context, opts agentstream.SupervisorOptions) int {
	return agentstream.RunSupervisor(ctx, opts)
}
