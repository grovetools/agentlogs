# Practical Examples

This guide provides practical examples of how to use `grove-claude-logs` (`clogs`) in common development workflows, from simple command-line inspection to programmatic analysis and integration with the Grove ecosystem.

## Example 1: Basic CLI Usage

This example covers the fundamental workflow for inspecting and querying Claude session logs from the command line. This is the most common use case for developers who need to review an agent's work or debug a `grove-flow` plan.

#### 1. List All Sessions

To get an overview of all your recent Claude sessions, use the `clogs list` command. It scans for transcript files and displays key metadata.

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

This table shows each session's ID, the associated project and worktree, any `grove-flow` jobs that were run, and when the session started.

#### 2. Filter for a Specific Project or Plan

If you have many sessions, you can filter the list with the `--project` (or `-p`) flag. This performs a substring match against the project, worktree, plan, and job names.

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

The most powerful feature for debugging is `clogs read`, which isolates the conversation for a single `grove-flow` job. This lets you see exactly what the agent did for a specific task.

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

This output provides the exact conversation transcript related to the specified job, making it easy to review the agent's actions and reasoning.

#### 4. Query Messages for Scripting

You can retrieve messages in a structured format using `clogs query`. This is useful for scripting or programmatic analysis. The following command gets all of the assistant's messages from a session as a JSON array.

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

For more advanced use cases, you can use `clogs` as a Go library to build custom analysis tools. This example demonstrates how to write a simple program to parse a session and count the number of times the agent used a specific tool.

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

This program demonstrates how to use the library to find, parse, and analyze transcript files, giving you the ability to build sophisticated custom monitoring and observability tools.

## Example 3: Integration with Grove Flow

`clogs` is designed to integrate deeply with the Grove ecosystem, particularly `grove-flow`. A key integration point is preserving the full execution history of an `interactive_agent` job by appending its Claude transcript to the job's Markdown file.

#### Scenario

You have an `agent` job in a `grove-flow` plan. After the agent completes its task, you want a permanent, auditable record of the entire interaction stored within the plan itself.

#### The Workflow

1.  **Run the Agent Job**: You start the process as usual with `grove-flow`.
    ```bash
    # This command will trigger an agent that interacts with Claude
    flow plan run my-feature-plan/01-implement-api.md
    ```

2.  **Complete the Job**: After the agent has finished its work (e.g., written code, fixed a bug), you mark the job as complete.
    ```bash
    flow plan complete my-feature-plan/01-implement-api.md
    ```

3.  **Automatic Transcript Append**: The `flow plan complete` command automatically performs several actions. One of these is to call `clogs read my-feature-plan/01-implement-api.md` under the hood. It captures the formatted output and appends it to the `01-implement-api.md` file.

#### Result

The job's Markdown file is updated to include the full conversation.

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

This integration ensures that your `grove-flow` plan directory becomes a self-contained, complete historical record of the work performed, which is invaluable for code reviews, auditing, and future reference.
