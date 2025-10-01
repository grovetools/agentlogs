<!-- DOCGEN:OVERVIEW:START -->

# Grove Claude Logs

`grove-claude-logs` (`clogs`) is a command-line tool and Go library for parsing, monitoring, and analyzing local Claude AI session transcripts. It provides a structured way to inspect, query, and understand interaction history with Claude, particularly within workflows orchestrated by `grove-flow`.

<!-- placeholder for animated gif -->

### Key Features

*   **Session Listing and Filtering**: Lists session transcripts and filters them by project, worktree, or job name.
*   **Targeted Log Reading**: Reads the conversation log for a specified `grove-flow` job.
*   **Message Querying**: Filters messages within a session by role (`user` or `assistant`) and provides JSON output.
*   **Real-Time Monitoring**: Provides a Go library for monitoring transcript files for new messages.
*   **Grove Integration**: Discovers transcripts and extracts metadata related to Grove projects, worktrees, and plans.

## How It Works

The `clogs` tool functions by scanning the `~/.claude/projects/` directory for `*.jsonl` files, where each file represents a session transcript. It parses these files line by line to extract messages and metadata, including the session ID and the original working directory. `grove-flow` job associations are determined by parsing user prompts within the transcript that match the execution pattern of an agent job, allowing `clogs` to link segments of a conversation to specific tasks in a plan.

## Ecosystem Integration

`clogs` is an observability component within the Grove ecosystem that provides data on LLM agent behavior.

*   **`grove-flow`**: `clogs` is used to review the execution of `grove-flow` plans. Since agents write their interactions to Claude transcripts, `clogs` allows a developer to debug a plan by reading the conversation that occurred for a specific job (e.g., `clogs read my-plan/01-setup.md`).
*   **`grove-hooks`**: `clogs` can act as a data source for the wider ecosystem. Its ability to parse and monitor transcripts can produce structured events about LLM interactions. These events can be published via `grove-hooks` to be consumed by other tools, such as dashboards or automated analysis agents.

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

<!-- DOCGEN:OVERVIEW:END -->

<!-- DOCGEN:TOC:START -->

See the [documentation](docs/) for detailed usage instructions:
- [Overview](docs/01-overview.md)
- [Examples](docs/02-examples.md)
- [Command Reference](docs/03-command-reference.md)

<!-- DOCGEN:TOC:END -->
