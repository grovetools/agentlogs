Document the configuration for the AI summarization feature. Use `internal/transcript/summary.go` as the source of truth, specifically the `loadSummaryConfig` function.

- Explain that configuration is loaded from `~/.config/tmux-claude-hud/config.yaml`.
- Document the YAML structure under the `conversation_summarization` key.
- Describe each field in the `SummaryConfig` struct: `enabled`, `llm_command`, `update_interval`, `current_window`, etc., explaining what each one controls.
- Provide a complete example `config.yaml` file.