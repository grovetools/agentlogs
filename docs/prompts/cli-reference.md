Generate a detailed command reference for the `clogs` CLI. Analyze `main.go` to document every command and its flags.

For each command (`list`, `read`, `query`, `tail`, `version`):
- Provide a clear description of what it does.
- List all available flags (e.g., `--json`, `--project`, `--session`, `--role`).
- Provide at least one clear example of how to use the command with its flags.
- For the `list` command, explain both the default table output and the JSON output format.
- For the `read` command, clearly explain the `plan/job` argument format and how it finds the relevant section of the log.