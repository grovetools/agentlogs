# Using Clogs as a Go Library

The `clogs` project provides a public Go library for developers who need to programmatically parse, monitor, or analyze Claude AI session transcripts. The library exposes its functionality through the `pkg/claudelogs` package.

The primary components of the library are the `Parser` for reading transcript files and the `Monitor` for real-time processing of active sessions.

## 1. Parsing a Transcript File

The `claudelogs.Parser` can read and process a `.jsonl` transcript file, extracting a structured representation of each message. This is useful for batch processing or analysis of historical log data.

The `ParseFile` function returns a slice of `transcript.ExtractedMessage` structs. Note that this requires importing a type from an `internal` package, which is necessary to access the detailed message structure.

### Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/mattsolo1/grove-claude-logs/internal/transcript"
	"github.com/mattsolo1/grove-claude-logs/pkg/claudelogs"
)

func main() {
	// For this example, we assume a transcript file exists.
	// In a real application, you might find this path dynamically.
	// You can use claudelogs.GetTranscriptPath(sessionID) to find it.
	const transcriptPath = "/path/to/your/home/.claude/projects/your-project/your-session-id.jsonl"

	// Create a new parser.
	parser := claudelogs.NewParser()

	// Parse the entire transcript file.
	messages, err := parser.ParseFile(transcriptPath)
	if err != nil {
		log.Fatalf("Failed to parse transcript file: %v", err)
	}

	fmt.Printf("Successfully parsed %d messages.\n\n", len(messages))

	// Print the role and content of each message.
	for _, msg := range messages {
		fmt.Printf("[%s] Role: %s\n", msg.Timestamp.Format("2006-01-02 15:04:05"), msg.Role)
		fmt.Printf("Content: %.100s...\n\n", msg.Content)
	}

	// You can also parse from an offset to read only new messages.
	// newMessages, newOffset, err := parser.ParseFileFromOffset(transcriptPath, 0)
}
```

## 2. Setting Up the Monitor

The `claudelogs.Monitor` provides a higher-level abstraction for real-time processing. It periodically scans for active Claude sessions, reads new messages since the last check, and stores them in a database.

The monitor requires a `*sql.DB` connection to manage its state, such as file offsets and message storage.

### Example

```go
package main

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3" // Example using SQLite
	"github.com/mattsolo1/grove-claude-logs/pkg/claudelogs"
)

func main() {
	// The monitor requires a database connection.
	// For this example, we use an in-memory SQLite database.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Initialize the monitor to check for new messages every 30 seconds.
	monitor := claudelogs.NewMonitor(db, 30*time.Second)

	// Start the monitor in a background goroutine.
	log.Println("Starting transcript monitor...")
	go monitor.Start()

	// Let the monitor run for a couple of minutes in this example.
	// In a real service, this would run indefinitely.
	log.Println("Monitor is running. Waiting for 2 minutes before shutdown...")
	time.Sleep(2 * time.Minute)

	// To shut down gracefully, call Stop().
	log.Println("Stopping monitor...")
	monitor.Stop()
	log.Println("Monitor stopped.")
}
```

## 3. Configuring the Monitor with AI Summarization

The monitor's behavior can be customized, including its experimental AI summarization feature. By using `claudelogs.NewMonitorWithConfig`, you can provide a `claudelogs.SummaryConfig` struct to control how and when session summaries are generated.

### Example

```go
package main

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mattsolo1/grove-claude-logs/pkg/claudelogs"
)

func main() {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Define a custom configuration for AI summarization.
	summaryConfig := claudelogs.SummaryConfig{
		Enabled:        true,
		LLMCommand:     "llm -m gpt-4o-mini", // Command to execute for summarization.
		UpdateInterval: 10,                   // Generate a summary every 10 messages.
		CurrentWindow:  10,                   // Use the last 10 messages for the "current activity" summary.
		MaxInputTokens: 8000,                 // Max tokens to feed to the LLM.
	}

	// Create a new monitor with the custom configuration.
	monitor := claudelogs.NewMonitorWithConfig(db, 30*time.Second, summaryConfig)

	// Start and stop the monitor as in the previous example.
	log.Println("Starting configured monitor...")
	go monitor.Start()

	time.Sleep(2 * time.Minute)

	log.Println("Stopping monitor...")
	monitor.Stop()
	log.Println("Monitor stopped.")
}
```