# Examples

This guide provides practical examples of using `grove-claude-logs` (`clogs`) in common development workflows, from command-line inspection to programmatic analysis.

## Example 1: Basic CLI Usage

This example covers the fundamental workflow for inspecting and querying Claude session logs from the command line. This is a common use case for reviewing an agent's work or debugging a `grove-flow` plan.

#### 1. List All Sessions

To get an overview of recent Claude sessions, use the `clogs list` command. It scans transcript files and displays metadata.

```bash
clogs list
```

**Expected Output:**

```
SESSION ID       PROJECT        WORKTREE      JOBS                  STARTED
session-gamma    my-api-project feature-x      refactor-plan/01.md   2025-09-26 15:30
session-beta     my-api-project               debug-plan/02.md      2025-09-26 14:00
session-alpha    another-proj                                       2025-09-25 11:00
```

The table shows each session's ID, associated project, worktree, any `grove-flow` jobs run, and when the session started.

#### 2. Filter for a Specific Project or Plan

The list can be filtered with the `--project` (or `-p`) flag. This performs a substring match against the project, worktree, plan, and job names.

```bash
clogs list --project my-api
```

**Expected Output:**

```
SESSION ID       PROJECT        WORKTREE      JOBS                  STARTED
session-gamma    my-api-project feature-x      refactor-plan/01.md   2025-09-26 15:30
session-beta     my-api-project               debug-plan/02.md      2025-09-26 14:00
```

#### 3. Read the Log for a Specific Job

The `clogs read` command isolates the conversation for a single `grove-flow` job. This shows the interaction that occurred for a specific task.

```bash
clogs read refactor-plan/01.md
```

**Expected Output:**

```
=== Job: refactor-plan/01.md ===
Project: my-api-project
Session: session-gamma
Starting at line: 42

User: Read the file src/main.go and execute the agent job.

Agent: [Using read_file on src/main.go]
Agent: Okay, I have read the file. I will now refactor the `handleRequest` function to improve error handling.

Agent: [Using apply_patch on src/main.go]

User: Looks good. Please write a unit test for the new error case.

...
=== End of session ===
```

This output provides the conversation transcript related to the specified job for reviewing the agent's actions.

#### 4. Query Messages for Scripting

Messages can be retrieved in a structured format using `clogs query`. The following command gets all of the assistant's messages from a session as a JSON array.

```bash
clogs query session-gamma --role assistant --json
```

**Expected Output:**

```json
[
  {
    "SessionID": "session-gamma",
    "MessageID": "msg_01J6...",
    "Timestamp": "2025-09-26T15:31:00Z",
    "Role": "assistant",
    "Content": "Okay, I have read the file...",
    "RawContent": "...",
    "Metadata": { ... }
  }
]
```

## Example 2: Programmatic Analysis with the Go Library

`clogs` can be used as a Go library to build analysis tools. This example demonstrates a program that parses a session and counts the number of times the agent used a specific tool.

#### Scenario

You want to analyze an agent's behavior in `session-gamma` to see how many times it modified files using the `apply_patch` tool.

#### Go Program

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/mattsolo1/grove-claude-logs/pkg/claudelogs"
)

func main() {
	sessionID := "session-gamma"
	toolNameToCount := "apply_patch"
	toolUseCount := 0

	// 1. Get the path to the transcript file
	path, err := claudelogs.GetTranscriptPath(sessionID)
	if err != nil {
		log.Fatalf("Could not find transcript for session %s: %v", sessionID, err)
	}

	// 2. Create a new parser and parse the file
	parser := claudelogs.NewParser()
	messages, err := parser.ParseFile(path)
	if err != nil {
		log.Fatalf("Failed to parse transcript: %v", err)
	}

	// 3. Iterate through messages and analyze content
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}

		// Assistant messages can have multiple content blocks (text, tool_use)
		var contentBlocks []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}

		if err := json.Unmarshal(msg.RawContent, &contentBlocks); err == nil {
			for _, block := range contentBlocks {
				if block.Type == "tool_use" && block.Name == toolNameToCount {
					toolUseCount++
				}
			}
		}
	}

	fmt.Printf("Analysis complete for session %s:\n", sessionID)
	fmt.Printf("The '%s' tool was used %d times.\n", toolNameToCount, toolUseCount)
}
```

This program shows how to use the library to find, parse, and analyze transcript files.

## Example 3: Integration with Grove Flow

A key integration point with `grove-flow` is appending the full Claude transcript to an `interactive_agent` job's Markdown file upon completion.

#### Scenario

You have an `agent` job in a `grove-flow` plan. After the agent completes its task, you want a record of the interaction stored within the plan itself.

#### The Workflow

1.  **Run the Agent Job**: Start the process as usual with `grove-flow`.
    ```bash
    # This command will trigger an agent that interacts with Claude
    flow plan run my-feature-plan/01-implement-api.md
    ```

2.  **Complete the Job**: After the agent has finished its work, you mark the job as complete.
    ```bash
    flow plan complete my-feature-plan/01-implement-api.md
    ```

3.  **Automatic Transcript Append**: The `flow plan complete` command calls `clogs read my-feature-plan/01-implement-api.md` under the hood. It captures the formatted output and appends it to the `01-implement-api.md` file.

#### Result

The job's Markdown file is updated to include the conversation.

**`01-implement-api.md` (Before `flow plan complete`):**
```markdown
---
id: job-xyz
title: Implement API
type: agent
status: running
---

Please implement the user API endpoint as discussed.
```

**`01-implement-api.md` (After `flow plan complete`):**
```markdown
---
id: job-xyz
title: Implement API
type: agent
status: completed
summary: "Implemented the /users endpoint and added basic CRUD operations."
---

Please implement the user API endpoint as discussed.

---
## Agent Transcript
*Appended by `grove-flow` on 2025-09-26*

=== Job: my-feature-plan/01-implement-api.md ===
Project: my-api-project
Session: session-gamma
Starting at line: 85

User: Implement the user API endpoint.

Agent: [Using read_file on api/routes.go]
Agent: I will add the new route to `api/routes.go` and create a new handler in `api/handlers/user.go`.
...
```

This integration ensures that the `grove-flow` plan directory becomes a self-contained historical record of the work performed.
