package display

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	grovelogging "github.com/mattsolo1/grove-core/logging"
)

var ulogCodex = grovelogging.NewUnifiedLogger("grove-agent-logs.display.codex")

// DisplayCodexLogLine parses and displays a Codex log line
func DisplayCodexLogLine(line []byte) {
	var entry map[string]interface{}
	if err := json.Unmarshal(line, &entry); err != nil {
		return // Skip lines that aren't valid JSON
	}

	payload, ok := entry["payload"].(map[string]interface{})
	if !ok {
		return
	}

	entryType, _ := payload["type"].(string)

	switch entryType {
	case "message":
		role, _ := payload["role"].(string)
		contentList, _ := payload["content"].([]interface{})
		var textContent string
		for _, c := range contentList {
			if cMap, ok := c.(map[string]interface{}); ok {
				if cType, ok := cMap["type"].(string); ok && cType == "input_text" {
					if text, ok := cMap["text"].(string); ok {
						textContent += text
					}
				}
			}
		}
		if textContent != "" && !strings.Contains(textContent, "<environment_context>") {
			ctx := context.Background()
			roleDisplay := "User"
			if role == "assistant" {
				roleDisplay = "Agent"
			}
			ulogCodex.Info("Message").
				Field("role", roleDisplay).
				Pretty(fmt.Sprintf("%s: %s\n\n", roleDisplay, textContent)).
				PrettyOnly().
				Log(ctx)
		}
	case "agent_message":
		if message, ok := payload["message"].(string); ok {
			ctx := context.Background()
			ulogCodex.Info("Agent message").
				Field("role", "Agent").
				Pretty(fmt.Sprintf("Agent: %s\n\n", message)).
				PrettyOnly().
				Log(ctx)
		}
	case "agent_reasoning":
		if text, ok := payload["text"].(string); ok {
			ctx := context.Background()
			ulogCodex.Info("Reasoning").
				Pretty(fmt.Sprintf("[Reasoning: %s]\n\n", text)).
				PrettyOnly().
				Log(ctx)
		}
	case "tool_code":
		if code, ok := payload["code"].(string); ok {
			ctx := context.Background()
			lang, _ := payload["language"].(string)
			ulogCodex.Info("Tool code").
				Field("language", lang).
				Pretty(fmt.Sprintf("[Tool (%s)]:\n%s\n\n", lang, code)).
				PrettyOnly().
				Log(ctx)
		}
	}
}
