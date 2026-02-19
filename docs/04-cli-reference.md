# CLI Reference

Complete command reference for `aglogs`.

## aglogs

<div class="terminal">
Agent transcript log parsing and monitoring

Usage:
  aglogs [command]

Available Commands:
  completion       Generate the autocompletion script for the specified shell
  help             Help about any command
  list             List available session transcripts
  query            Query messages from a transcript
  read             Read logs for a specific job, session, or log file
  tail             Tail and parse messages from a specific transcript
  version          Print the version information for this binary

Flags:
  -c, --config string   Path to grove.yml config file
  -h, --help            help for aglogs
      --json            Output in JSON format
  -v, --verbose         Enable verbose logging

Use "aglogs [command] --help" for more information about a command.
</div>

### aglogs list

<div class="terminal">
List available session transcripts, optionally filtered by project name

Usage:
  aglogs list [flags]

Flags:
  -h, --help             help for list
      --json             Output in JSON format
  -p, --project string   Filter sessions by project, worktree, plan, or job name (case-insensitive substring match)

Global Flags:
  -c, --config string   Path to grove.yml config file
  -v, --verbose         Enable verbose logging
</div>

### aglogs query

<div class="terminal">
Query messages from a transcript

Usage:
  aglogs query &lt;session_id&gt; [flags]

Flags:
  -h, --help          help for query
      --json          Output in JSON format
      --role string   Filter by message role (user, assistant)

Global Flags:
  -c, --config string   Path to grove.yml config file
  -v, --verbose         Enable verbose logging
</div>

### aglogs read

<div class="terminal">
Reads logs for a job execution. &lt;spec&gt; can be a plan/job, a session ID, or a direct path to a job or log file.

Usage:
  aglogs read &lt;spec&gt; [flags]

Flags:
      --detail string   Set detail level for output ('summary' or 'full'). Overrides config.
  -h, --help            help for read
      --json            Output in JSON format with additional metadata

Global Flags:
  -c, --config string   Path to grove.yml config file
  -v, --verbose         Enable verbose logging
</div>

### aglogs tail

<div class="terminal">
Tail and parse messages from a specific transcript

Usage:
  aglogs tail &lt;session_id&gt; [flags]

Flags:
  -h, --help   help for tail

Global Flags:
  -c, --config string   Path to grove.yml config file
      --json            Output in JSON format
  -v, --verbose         Enable verbose logging
</div>

### aglogs version

<div class="terminal">
Print the version information for this binary

Usage:
  aglogs version [flags]

Flags:
  -h, --help   help for version
      --json   Output version information in JSON format

Global Flags:
  -c, --config string   Path to grove.yml config file
  -v, --verbose         Enable verbose logging
</div>

