# Grove Claude Logs (`clogs`)

`grove-claude-logs` (`clogs`) is a command-line tool and Go library for parsing, monitoring, and analyzing local Claude AI session transcripts. As part of the Grove developer tool ecosystem, it provides a structured way for developers to inspect, query, and understand their interaction history with Claude, particularly within workflows orchestrated by `grove-flow`.

<!-- placeholder for animated gif -->

### Key Features

*   **Session Listing and Filtering**: Browse all Claude session transcripts with metadata like project, worktree, and associated `grove-flow` jobs. Filter the list to quickly find relevant sessions.
*   **Targeted Log Reading**: Read the specific conversation log for a single `grove-flow` job, isolating the interaction for a particular task.
*   **Message Querying**: Search and filter messages within a session by role (`user` or `assistant`), with support for structured JSON output for scripting.
*   **Real-Time Monitoring**: The underlying Go library can be used to monitor transcript files for new messages as they are written, enabling real-time applications.
*   **Grove Integration**: Designed to work with the Grove ecosystem, `clogs` automatically discovers transcripts and extracts metadata related to Grove projects, worktrees, and plans.

## Ecosystem Integration

`clogs` is a key observability component within the Grove ecosystem, providing critical insights into the behavior of LLM agents.

*   **`grove-flow`**: The primary use case for `clogs` is to review the execution of `grove-flow` plans. Since agents write their interactions to Claude transcripts, `clogs` allows you to debug a plan by reading the exact conversation that occurred for a specific job (e.g., `clogs read my-plan/01-setup.md`).
*   **`grove-hooks`**: `clogs` acts as a data source for the wider ecosystem. Its ability to parse and monitor transcripts in real-time means it can produce structured events about LLM interactions. These events can be published via `grove-hooks` to be consumed by other tools, such as dashboards, alerting systems, or automated analysis agents.

By providing a structured interface to raw transcript data, `clogs` makes LLM interactions a first-class, observable part of the development workflow.

## Installation

Install via the Grove meta-CLI:
```bash
grove install claude-logs
```

Verify installation:
```bash
clogs version
```

Requires the `grove` meta-CLI. See the [Grove Installation Guide](https://github.com/mattsolo1/grove-meta/blob/main/docs/02-installation.md) if you don't have it installed.