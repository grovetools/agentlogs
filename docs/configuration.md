# Configuration

This document describes how to configure the AI summarization feature for grove-claude-logs.

## Configuration File Location

Configuration is loaded from `~/.config/tmux-claude-hud/config.yaml`. This file contains settings for various features including the AI conversation summarization system.

## Configuration Structure

The AI summarization feature is configured under the `conversation_summarization` key in the YAML file. All summarization settings are contained within this section.

## Configuration Fields

The `conversation_summarization` section supports the following fields:

### `enabled` (boolean)
- **Default**: `false`
- **Description**: Controls whether AI summarization is active. When set to `false`, no summaries will be generated regardless of other settings.

### `llm_command` (string)
- **Default**: `"llm -m gpt-4o-mini"`
- **Description**: Specifies the command used to invoke the LLM for generating summaries. This should be a complete command that accepts input via stdin and outputs the summary to stdout.

### `update_interval` (integer)
- **Default**: `10`
- **Description**: Determines how frequently summaries are updated. A summary will be regenerated every N new messages in the conversation.

### `current_window` (integer)
- **Default**: `10`
- **Description**: Sets the number of recent messages to analyze when generating the "current activity" summary. This focuses on the most immediate work being done.

### `recent_window` (integer)
- **Default**: `30`
- **Description**: Defines the number of recent messages to consider for broader context when generating summaries. This provides more historical context than the current window.

### `max_input_tokens` (integer)
- **Default**: `8000`
- **Description**: Limits the maximum number of tokens sent to the LLM. Messages will be truncated if they exceed this limit to stay within the LLM's context window.

### `milestone_detection` (boolean)
- **Default**: `true`
- **Description**: Enables detection and tracking of significant milestones in the conversation. When enabled, the system maintains a history of important achievements and progress markers.

## Complete Example Configuration

Here's a complete example `config.yaml` file with AI summarization configured:

```yaml
# ~/.config/tmux-claude-hud/config.yaml

conversation_summarization:
  enabled: true
  llm_command: "llm -m gpt-4o-mini"
  update_interval: 10
  current_window: 10
  recent_window: 30
  max_input_tokens: 8000
  milestone_detection: true

# Other configuration sections can be added here
# For example:
# display:
#   theme: dark
#   refresh_interval: 5
```

## Configuration Notes

- If the configuration file doesn't exist or cannot be read, the system will use the default values shown above with `enabled: false`
- The `llm_command` should point to a working LLM installation. The default uses the `llm` CLI tool with the `gpt-4o-mini` model
- Token counting is approximate (roughly 3 characters per token) for the `max_input_tokens` limit
- The system maintains conversation history and generates progressive summaries that build upon previous summaries
- Summaries are stored in the database and persist across sessions

## LLM Command Requirements

The configured `llm_command` must:
- Accept input via stdin
- Output the generated summary to stdout
- Return a non-zero exit code on failure
- Be available in the system PATH or specified with a full path

Popular LLM CLI tools that work with this configuration include:
- `llm` (https://github.com/simonw/llm)
- `openai` CLI
- Custom wrapper scripts for other LLM APIs