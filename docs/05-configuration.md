## Agentlogs Settings

| Property | Description |
| :--- | :--- |
| `transcript` | (object, optional) Configuration settings specific to the formatting and display of agent transcripts. This section groups settings that control verbosity and output limits to help you manage log readability. |

### Transcript

| Property | Description |
| :--- | :--- |
| `detail_level` | (string, optional) Controls the level of detail shown in the transcript logs. Set this to "summary" for a concise view of tool usage, or "full" to inspect the complete input and output of every tool call, which is essential for deep debugging. |
| `max_diff_lines` | (integer, optional) Specifies the maximum number of lines to display for file diffs in the logs. If set to 0, the full diff is shown. Setting a positive integer allows you to truncate extensive file changes, keeping your terminal output cleaner and more manageable. |

```toml
[aglogs.transcript]
detail_level = "summary"
max_diff_lines = 20
```