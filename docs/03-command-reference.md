# Command Reference

This document provides a detailed reference for all commands available in the `clogs` command-line interface.

## clogs list

Lists all available Claude session transcripts found in the `~/.claude/projects/` directory.

The command scans transcript files to extract metadata such as the project name, worktree, and any associated Grove Flow jobs, then displays the sessions sorted by start time with the most recent first.

### Flags

| Flag      | Shorthand | Description                                                                   |
| :-------- | :-------- | :---------------------------------------------------------------------------- |
| `--json`  |           | Output the list of sessions in JSON format instead of a table.                |
| `--project` | `-p`      | Filter sessions by a case-insensitive substring match against the project name, worktree, plan, or job. |

### Output Formats

**Default Table Output**

By default, `clogs list` prints a human-readable table with key information about each session.

```
SESSION ID       PROJECT        WORKTREE      JOBS                  STARTED
session-alpha    project-alpha                my-plan/01-setup.md   2006-01-02 15:04
session-beta     project-beta   feature-x                           2006-01-01 12:00
```

**JSON Output**

When the `--json` flag is used, the command outputs a JSON array of session objects.

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
    "startedAt": "2006-01-02T15:04:05Z"
  }
]
```

### Examples

**List all sessions:**
```bash
clogs list
```

**List sessions for a specific project or plan:**
```bash
clogs list --project my-project
```

**Get the list of all sessions in JSON format:**
```bash
clogs list --json
```

---

## clogs read

Reads and displays the conversation log for a specific job within a Grove Flow plan.

The command searches through all session transcripts to find where the specified job was executed. It then prints the conversation starting from that job's invocation until the next job begins or the session ends.

### Argument: `<plan/job>`

The required argument must be in the format `plan-name/job-filename.md`. For example: `my-feature-plan/01-implement-api.md`.

### Flags

| Flag      | Shorthand | Description                                                    |
| :-------- | :-------- | :------------------------------------------------------------- |
| `--session` | `-s`      | Specify a session ID if multiple sessions contain the same job. |
| `--project` | `-p`      | Filter the search to sessions within a specific project.        |

### Examples

**Read the log for a specific job:**
```bash
clogs read my-plan/02-write-tests.md
```

**Read the log for a job within a specific project to narrow the search:**
```bash
clogs read my-plan/02-write-tests.md --project my-api
```
If multiple sessions are found for the same job, `clogs` will list them and prompt you to re-run the command with the `--session` flag.

---

## clogs query

Queries and filters messages from a specific session transcript.

This command allows you to retrieve all messages from a session or filter them by role (`user` or `assistant`). The output can be formatted as plain text or structured JSON.

### Argument: `<session_id>`

The required argument is the session ID of the transcript to query.

### Flags

| Flag     | Shorthand | Description                                        |
| :------- | :-------- | :------------------------------------------------- |
| `--role` |           | Filter messages by role: `user` or `assistant`.    |
| `--json` |           | Output the matching messages in JSON format.       |

### Examples

**Query all messages from a session:**
```bash
clogs query session-alpha
```

**Query only the user's messages from a session:**
```bash
clogs query session-alpha --role user
```

**Get all of the assistant's messages as JSON:**
```bash
clogs query session-alpha --role assistant --json
```

---

## clogs tail

Displays the last few messages from a specific session transcript.

This provides a way to see the most recent activity in a session without viewing the entire log. It currently shows the last 10 messages.

### Argument: `<session_id>`

The required argument is the session ID of the transcript to tail.

### Flags

This command has no flags.

### Examples

**Tail a specific session:**
```bash
clogs tail session-alpha
```

---

## clogs version

Prints the version information for the `clogs` binary.

This command displays the build version, commit hash, branch, and build date.

### Flags

| Flag     | Shorthand | Description                            |
| :------- | :-------- | :------------------------------------- |
| `--json` |           | Output version information in JSON format. |

### Examples

**Show the version information:**
```bash
clogs version
```

**Get the version information as JSON:**
```bash
clogs version --json
```