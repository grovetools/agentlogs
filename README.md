# Clogs: Claude Log Manager

Clogs is both a CLI tool and a Go library for parsing, monitoring, and analyzing local Claude AI session transcripts. It's part of the "Grove" developer tool ecosystem, designed to help developers understand and work with their Claude conversation history.

## Key Features

### CLI Tool
- **List sessions:** Browse all Claude session transcripts with filtering by project, worktree, plan, or job name
- **Read specific executions:** View logs for specific plan/job executions with clear, formatted output
- **Tail recent activity:** Display the last messages from any session
- **Query and filter:** Search messages by role (user/assistant) with JSON output support
- **Grove integration:** Seamlessly integrates with Grove Flow plans and job executions

### Go Library
- **Real-time monitoring:** Monitor Claude transcript files for new messages as they arrive
- **Robust parsing:** Parse JSONL transcript files with support for different message formats
- **Database integration:** Store and manage extracted messages with SQLite support
- **Automatic summarization:** Generate session summaries and milestone tracking
- **Configurable extraction:** Customizable parsing and monitoring behavior

## Quick Start

The most common CLI commands to get started:

```bash
# List all available session transcripts
clogs list

# Filter sessions by project name
clogs list -p my-project

# Read logs for a specific plan/job execution
clogs read my-plan/01-setup.md

# Follow the last messages from a session
clogs tail <session-id>

# Query messages with filtering
clogs query <session-id> --role assistant --json
```

## Installation

Installation is typically handled via the grove meta-tool, which manages binaries across the entire Grove ecosystem. You can also download binaries directly from the [GitHub Releases](https://github.com/mattsolo1/grove-claude-logs/releases) page.

To build from source:
```bash
make build
```

This creates the binary in `./bin/clogs`. The Grove ecosystem handles binary discovery automatically, so no need to add to your PATH.

## Usage as a Library

Clogs can be used as a Go library for custom applications that need to parse or monitor Claude transcripts. For detailed documentation on the Go library API, including examples for setting up monitors, configuring parsers, and handling real-time transcript processing, see [docs/go-library-usage.md](docs/go-library-usage.md).

## Full Documentation

For complete documentation, including advanced CLI usage, library API reference, and integration guides, see the [docs](docs/) directory.