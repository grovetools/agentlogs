Based on the `internal/transcript` directory, document the core concepts of the `clogs` system.

1. **Claude Transcripts:** Explain the source of the data: the `.jsonl` files located in `~/.claude/projects`. Describe the basic structure of a `TranscriptEntry` from `parser.go`.
2. **Message Parsing:** Detail how the `Parser` reads these files, handles large lines, and extracts a simplified `ExtractedMessage` struct.
3. **Real-time Monitoring:** Describe the role of the `Monitor` from `monitor.go`. Explain how it periodically checks active sessions, uses file offsets to avoid reprocessing data, and stores messages in a database.
4. **AI Summarization:** Explain the `SummaryManager` from `summary.go`. Detail its purpose, how it's triggered based on message intervals, and that it calls an external LLM command to generate summaries.