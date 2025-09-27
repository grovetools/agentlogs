Create a guide for developers on how to use `clogs` as a Go library. Reference the `pkg/claudelogs` package as the public API.

Provide Go code examples for the following:
1. **Parsing a Transcript File:** Show how to create a new `claudelogs.Parser` and use it to parse a `.jsonl` file to get a slice of messages.
2. **Setting up the Monitor:** Demonstrate how to initialize `claudelogs.NewMonitor` with a `*sql.DB` connection and a check interval. Show how to start and stop it gracefully (`monitor.Start()`, `monitor.Stop()`).
3. **Configuring the Monitor:** Show an example of using `claudelogs.NewMonitorWithConfig` to customize the AI summarization behavior by passing a `claudelogs.SummaryConfig` struct.