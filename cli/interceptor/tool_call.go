package interceptor

import (
	"encoding/json"
	"regexp"
	"strings"
)

// toolCallInterceptor extracts tool calls from thinking blocks.
type toolCallInterceptor struct {
	style             APIStyle
	thinkingPatterns  []*regexp.Regexp
	toolCallPattern   *regexp.Regexp
}

func newToolCallInterceptor(style APIStyle) *toolCallInterceptor {
	return &toolCallInterceptor{
		style: style,
		thinkingPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?s)<thinking\b[^>]*>(.*?)</thinking>`),
			regexp.MustCompile(`(?s)<think>(.*?)</think>`),
		},
		toolCallPattern: regexp.MustCompile(
			`(?s)<(?:tool_call|function_call|tool_use)\b[^>]*(?:name=["\']([^"\']+)["\'][^>]*>(.*?)</?(?:tool_call|function_call|tool_us[et])?>`,
		),
	}
}

func (t *toolCallInterceptor) Feed(chunk []byte) ([]byte, error) {
	line := string(chunk)

	switch t.style {
	case StyleOllama:
		return []byte(t.processOllama(line)), nil
	case StyleOpenAIChat:
		return []byte(t.processOpenAIChat(line)), nil
	default:
		return chunk, nil
	}
}

func (t *toolCallInterceptor) ShouldContinue() bool {
	return false
}

func (t *toolCallInterceptor) Reset() {
	// No state to reset
}

func (t *toolCallInterceptor) processOllama(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return line
	}

	var parts struct {
		Done bool `json:"done"`
		Response string `json:"response,omitempty"`
		Message *struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message,omitempty"`
	}

	if err := json.Unmarshal([]byte(line), &parts); err != nil {
		return line
	}

	if parts.Done {
		return line
	}

	var content string
	switch {
	case parts.Response != "":
		content = parts.Response
	case parts.Message != nil && parts.Message.Content != "":
		content = parts.Message.Content
	default:
		return line
	}

	_, cleaned := t.extractFromThinking(content)
	if cleaned == content {
		return line
	}

	var result map[string]any
	json.Unmarshal([]byte(line), &result)

	switch {
	case result["response"] != nil:
		result["response"] = cleaned
	case result["message"] != nil:
		msg, ok := result["message"].(map[string]any)
		if ok {
			msg["content"] = cleaned
		}
	}

	modified, _ := json.Marshal(result)
	return string(modified)
}

func (t *toolCallInterceptor) processOpenAIChat(line string) string {
	if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
		return line
	}

	data := strings.TrimPrefix(line, "data: ")
	
	var parts struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(data), &parts); err != nil {
		return line
	}

	if len(parts.Choices) == 0 {
		return line
	}

	content := parts.Choices[0].Delta.Content
	_, cleaned := t.extractFromThinking(content)

	parts.Choices[0].Delta.Content = cleaned
	
	modified, _ := json.Marshal(parts)
	return "data: " + string(modified) + "\n"
}

func (t *toolCallInterceptor) extractFromThinking(content string) ([]string, string) {
	var extracted []string

	for _, pattern := range t.thinkingPatterns {
		matches := pattern.FindAllStringSubmatchIndex(content, -1)
		if len(matches) == 0 {
			continue
		}

		for idx := len(matches) - 1; idx >= 0; idx-- {
			match := matches[idx]
			fullStart, fullEnd := match[0], match[1]
			thinkingStart, thinkingEnd := match[2], match[3]

			thinkingContent := content[thinkingStart:thinkingEnd]
			
			tcMatches := t.toolCallPattern.FindAllString(thinkingContent, -1)
			for _, tcMatch := range tcMatches {
				extracted = append(extracted, tcMatch)
				thinkingContent = strings.Replace(thinkingContent, tcMatch, "", 1)
			}

			content = content[:fullStart] + thinkingContent + content[fullEnd:]
		}
	}

	return extracted, content
}
