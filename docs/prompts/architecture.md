Briefly document the high-level architecture of the `clogs` project.

Explain the purpose of each top-level directory:
- `cmd/`: Contains the entry point and command definitions for the `clogs` CLI, using the `cobra` library.
- `internal/`: Holds the core application logic (parsing, monitoring, summarization) that is not meant to be imported by other projects.
- `pkg/`: Exposes the core logic as a reusable public Go library (`claudelogs`) for other applications to consume.
- `tests/e2e/`: Contains the end-to-end tests for the CLI, using the `grove-tend` framework.