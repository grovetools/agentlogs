<!-- DOCGEN:OVERVIEW:START -->

`aglogs` (Agent Logs) is a command-line tool for parsing, monitoring, and analyzing transcripts from local AI agents. It normalizes proprietary log formats from multiple providers into a unified structure, enabling consistent inspection and integration with the Grove ecosystem.

## Core Mechanisms

**Unified Normalization**: The tool reads provider-specific log formats (typically JSONL) and converts them into a standard `UnifiedEntry` structure. This normalization handles differences in timestamp formatting, role definitions, and tool call representations (e.g., merging Claude's separate tool use and result events).

**Discovery Strategies**: `aglogs` locates session transcripts using a tiered approach:
1.  **Direct Path**: Accepts direct file paths to log files.
2.  **Session Registry**: Queries the `grove hooks` database (`~/.local/state/grove/hooks/sessions/`) to resolve Grove Job IDs to native agent session IDs.
3.  **Filesystem Scanning**: Scans default storage directories for supported providers to index available sessions.

**Output Formatting**:
*   **Human-Readable**: Renders formatted text with syntax highlighting, diff views for file edits, and distinct icons for tools vs. conversation.
*   **JSON**: Outputs raw structured data for machine consumption.

## Supported Providers

`aglogs` supports the following local agent runtimes:

*   **Claude Code**: Scans `~/.claude/projects/` for project-scoped JSONL transcripts.
*   **Codex**: Scans `~/.codex/sessions/` for session logs.
*   **OpenCode**: Assembles fragmented message and part files from `~/.local/share/opencode/storage/`.

## Integration with Grove Flow

`aglogs` serves as the observability layer for `flow` orchestrations.

**Live Streaming**: The `flow plan status` TUI executes `aglogs stream` to display real-time output from running agents directly within the terminal interface. This allows monitoring of headless and interactive agents without attaching to their specific processes.

**Transcript Archival**: Upon job completion, `flow` executes `aglogs read` to retrieve the full session history. This content is appended to the job's Markdown file (e.g., `01-impl.md`), preserving the chain of thought and execution history alongside the task definition.

**Session Resumption**: When resuming an interactive job, `flow` uses `aglogs get-session-info` to look up the native agent session ID associated with the job, enabling the agent to continue context from the previous run.

## Features

*   **`aglogs list`**: Indexes and displays available sessions across all providers. Supports filtering by project or job name.
*   **`aglogs read`**: Outputs the full log for a specific session ID, Job ID, or file path.
*   **`aglogs stream`**: Tails a live log file, rendering new entries as they are written.
*   **`aglogs query`**: Filters messages within a transcript by role (e.g., `--role user`) or content.

<!-- DOCGEN:OVERVIEW:END -->

<!-- DOCGEN:TOC:START -->

See the [documentation](docs/) for detailed usage instructions:
- [Overview](docs/01-overview.md)
- [Examples](docs/02-examples.md)
- [Command Reference](docs/03-command-reference.md)
- [Configuration](docs/05-configuration.md)

<!-- DOCGEN:TOC:END -->
