# CLI Command Reference

This document provides a detailed reference for all commands available in the `clogs` CLI.

---

## `clogs list`

The `list` command scans for and displays all available Claude session transcripts found in `~/.claude/projects/`.

### Description

This command provides a summary of each session, including its ID, associated project, worktree (if applicable), and start time. It's the primary way to get an overview of recent activity.

### Flags

| Flag                  | Shorthand | Description                                                                   |
| --------------------- | --------- | ----------------------------------------------------------------------------- |
| `--json`              |           | Output the list of sessions in JSON format instead of the default table.      |
| `--project <name>`    | `-p`      | Filter sessions by a case-insensitive substring match on the project, worktree, plan, or job name. |

### Output Formats

#### Default Table Output

By default, `clogs list` prints a formatted table that is easy to read in the terminal.

```
SESSION ID       PROJECT          WORKTREE    JOBS                           STARTED
session-alpha    project-alpha                my-plan/01-setup.md (+1 more)  2025-01-01 12:00
session-beta     project-beta                                                2025-01-02 10:00
```

#### JSON Output

When using the `--json` flag, the command outputs a JSON array of session objects. Each object contains detailed information about the session.

```json
[
  {
    "sessionId": "session-alpha",
    "projectName": "project-alpha",
    "projectPath": "/path/to/project-alpha",
    "worktree": "",
    "jobs": [
      {
        "plan": "my-plan",
        "job": "01-setup.md",
        "lineIndex": 25
      }
    ],
    "logFilePath": "/home/user/.claude/projects/project-alpha/session-alpha.jsonl",
    "startedAt": "2025-01-01T12:00:00Z"
  }
]
```

### Examples

**1. List all sessions:**

```bash
clogs list
```

**2. List sessions for a specific project:**

```bash
clogs list --project my-project-name
```

**3. List sessions and output as JSON:**

```bash
clogs list --json
```

---

## `clogs read`

The `read` command displays the conversation log for a specific `plan/job` execution within a session.

### Description

This command is useful for reviewing the detailed interaction for a single task. It locates the line in the transcript where the specified job was initiated and prints the conversation from that point until the next job begins or the session ends.

### Argument Format

The command requires a single argument in the format `<plan_name>/<job_file.md>`.

-   **`<plan_name>`**: The name of the plan directory (e.g., `feature-development`).
-   **`<job_file.md>`**: The markdown file name of the job (e.g., `01-implement-api.md`).

### Flags

| Flag                  | Shorthand | Description                                                               |
| --------------------- | --------- | ------------------------------------------------------------------------- |
| `--session <id>`      | `-s`      | Specify the session ID. This is required if multiple sessions contain the same job. |
| `--project <name>`    | `-p`      | Filter the search to sessions belonging to a specific project.              |

### Examples

**1. Read logs for a specific job:**

```bash
clogs read feature-development/01-implement-api.md
```

**2. Read logs for a job within a specific session (if ambiguous):**

```bash
clogs read feature-development/01-implement-api.md --session session-alpha
```

---

## `clogs query`

The `query` command allows for detailed inspection of messages within a specific session transcript, with options for filtering.

### Description

This command parses the entire transcript file for a given session and displays all messages. It can filter messages by role and output the results in either a human-readable format or structured JSON.

### Flags

| Flag            | Shorthand | Description                                           |
| --------------- | --------- | ----------------------------------------------------- |
| `--role <role>` |           | Filter messages by role (`user` or `assistant`).      |
| `--json`        |           | Output the resulting messages in JSON format.         |

### Examples

**1. Show all messages for a session:**

```bash
clogs query session-alpha
```

**2. Show only the assistant's messages:**

```bash
clogs query session-alpha --role assistant
```

**3. Get all user messages in JSON format:**

```bash
clogs query session-alpha --role user --json
```

---

## `clogs tail`

The `tail` command displays the most recent messages from a specific session transcript.

### Description

This is useful for quickly checking the latest activity in a session or for monitoring a session that is currently in progress. It shows the last 10 messages by default.

### Flags

This command has no flags.

### Examples

**1. Tail the log for a specific session:**

```bash
clogs tail session-alpha
```

---

## `clogs version`

The `version` command prints the version information for the `clogs` binary.

### Description

This command displays the build version, git commit, and build date, which is useful for debugging and reporting issues.

### Flags

| Flag     | Shorthand | Description                                           |
| -------- | --------- | ----------------------------------------------------- |
| `--json` |           | Output the version information in JSON format.        |

### Examples

**1. Display version information:**

```bash
clogs version
```

**2. Display version information as JSON:**

```bash
clogs version --json
```