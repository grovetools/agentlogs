// Package sessioninfo is the public seam over agentlogs' tiered session
// resolver (internal/session). External consumers — flow's monitoring TUI and
// completion-time archiving — resolve a job/session spec here instead of
// hand-building ~/.claude/projects paths, so resolution stays provider-aware
// (daemon job registry → daemon session lookup → opencode transcript pointer
// → full multi-provider scanner) with one implementation.
//
// The types are aliases of the internal ones (the smallest correct move: the
// resolver keeps living in internal/, nothing is duplicated, and in-module
// callers are untouched).
package sessioninfo

import (
	"github.com/grovetools/agentlogs/internal/session"
)

// Info describes a resolved session: identity, provider, transcript location
// (LogFilePath — for opencode this is the session INFO file under the storage
// root, not a transcript), and any flow jobs it served.
type Info = session.SessionInfo

// JobInfo identifies a flow plan job referenced by a session.
type JobInfo = session.JobInfo

// Resolve finds a session's metadata from a specifier: a flow job ID, a
// plan/job string ("<plan>/<job>.md"), a native session ID, or a direct
// path to a job file or transcript. Lookup tiers, fastest first: the daemon
// job registry, the daemon session store, the opencode transcript pointer in
// the hooks session registry, then a full multi-provider filesystem scan
// (claude, codex, pi, opencode).
func Resolve(spec string) (*Info, error) {
	return session.ResolveSessionInfo(spec)
}
