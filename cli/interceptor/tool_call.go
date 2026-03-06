package interceptor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// toolCallInterceptor extracts tool calls from thinking blocks.
type toolCallInterceptor struct {
	style            APIStyle
	thinkingPatterns []*regexp.Regexp
	toolCallPattern  *regexp.Regexp
}

func newToolCallInterceptor(style APIStyle) *toolCallInterceptor {
	return &toolCallInterceptor{
		style: style,
		thinkingPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?s)<thinking\b[^>]*>(.*?)</thinking>`),
			regexp.MustCompile(`(?s)<think>(.*?)</think>`),
		},
		toolCallPattern: regexp.MustCompile(
			`(?s)<(?:tool_call|function_call|tool_use)\b[^>]*name=["'][^"']+["'][^>]>(.*?)</?(?:tool_call|function_call|tool_use)?>`,
		),
	}
}

func (t *toolCallInterceptor) Feed(chunk []byte, logCb LogCallback) ([]byte, error) {
	line := string(chunk)

	switch t.style {
	case StyleOllama:
		modified, count := t.processOllama(line)
		if count > 0 && logCb != nil {
			logCb(fmt.Sprintf("[interceptor] Extracted %d tool call(s) from thinking blocks", count))
		}
		return []byte(modified), nil
	case StyleOpenAIChat:
		modified, count := t.processOpenAIChat(line)
		if count > 0 && logCb != nil {
			logCb(fmt.Sprintf("[interceptor] Extracted %d tool call(s) from thinking blocks", count))
		}
		return []byte(modified), nil
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

func (t *toolCallInterceptor) processOllama(line string) (string, int) {
	line = strings.TrimSpace(line)
	if line == "" {
		return line, 0
	}

	var parts struct {
		Done     bool   `json:"done"`
		Response string `json:"response,omitempty"`
		Message  *struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message,omitempty"`
	}

	if err := json.Unmarshal([]byte(line), &parts); err != nil {
		return line, 0
	}

	if parts.Done {
		return line, 0
	}

	var content string
	switch {
	case parts.Response != "":
		content = parts.Response
	case parts.Message != nil && parts.Message.Content != "":
		content = parts.Message.Content
	default:
		return line, 0
	}

	extracted, cleaned := t.extractFromThinking(content)
	if len(extracted) == 0 {
		return line, 0
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		return line, 0
	}

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
	return string(modified), len(extracted)
}

func (t *toolCallInterceptor) processOpenAIChat(line string) (string, int) {
	if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
		return line, 0
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
		return line, 0
	}

	if len(parts.Choices) == 0 {
		return line, 0
	}

	content := parts.Choices[0].Delta.Content
	extracted, cleaned := t.extractFromThinking(content)

	parts.Choices[0].Delta.Content = cleaned

	modified, _ := json.Marshal(parts)
	return "data: " + string(modified) + "\n", len(extracted)
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
