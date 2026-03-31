package agentstream

import (
	"context"

	"github.com/grovetools/core/pkg/daemon"
)

// RegisterOptions configures session registration with the daemon.
type RegisterOptions struct {
	JobID       string
	Provider    string
	WorkDir     string
	Title       string
	JobFilePath string
	PlanName    string
}

// Confirmer is returned by RegisterAgent and used to confirm the session
// after PID discovery and transcript path resolution.
type Confirmer struct {
	client daemon.Client
	opts   RegisterOptions
}

// RegisterAgent pre-registers a session intent with the daemon before agent launch.
// Returns a Confirmer that should be called after PID discovery.
func RegisterAgent(ctx context.Context, opts RegisterOptions) (*Confirmer, error) {
	client := daemon.NewWithAutoStart()
	err := client.RegisterSessionIntent(ctx, daemon.SessionIntent{
		JobID:       opts.JobID,
		Provider:    opts.Provider,
		JobFilePath: opts.JobFilePath,
		PlanName:    opts.PlanName,
		Title:       opts.Title,
		WorkDir:     opts.WorkDir,
	})
	if err != nil {
		return nil, err
	}
	return &Confirmer{client: client, opts: opts}, nil
}

// Confirm links the pre-registered intent with the actual running process.
func (c *Confirmer) Confirm(ctx context.Context, pid int, nativeID, transcriptPath string) error {
	return c.client.ConfirmSession(ctx, daemon.SessionConfirmation{
		JobID:          c.opts.JobID,
		NativeID:       nativeID,
		PID:            pid,
		TranscriptPath: transcriptPath,
	})
}

// Close releases any resources held by the confirmer's daemon client.
func (c *Confirmer) Close() error {
	return c.client.Close()
}
