package display

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
			roleDisplay := "User"
			if role == "assistant" {
				roleDisplay = "Agent"
			}
			fmt.Printf("%s: %s\n\n", roleDisplay, textContent)
		}
	case "agent_message":
		if message, ok := payload["message"].(string); ok {
			fmt.Printf("Agent: %s\n\n", message)
		}
	case "agent_reasoning":
		if text, ok := payload["text"].(string); ok {
			fmt.Printf("[Reasoning: %s]\n\n", text)
		}
	case "tool_code":
		if code, ok := payload["code"].(string); ok {
			lang, _ := payload["language"].(string)
			fmt.Printf("[Tool (%s)]:\n%s\n\n", lang, code)
		}
	}
}
