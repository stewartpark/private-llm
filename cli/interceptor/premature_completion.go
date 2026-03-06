package interceptor

import (
	"bytes"
	"encoding/json"
	"strings"
)

// prematureCompletionInterceptor detects if a response ended prematurely.
type prematureCompletionInterceptor struct {
	style            APIStyle
	lastContentType  string
	accumulatedBytes bytes.Buffer
	emptyResponse    bool // true if no content chunks received
}

func newPrematureCompletionInterceptor(style APIStyle) *prematureCompletionInterceptor {
	return &prematureCompletionInterceptor{
		style:           style,
		lastContentType: contentTypeEmpty,
	}
}

func (i *prematureCompletionInterceptor) Feed(chunk []byte, logCb LogCallback) ([]byte, error) {
	line := string(chunk)

	switch i.style {
	case StyleOllama:
		i.feedOllama(line)
	case StyleOpenAIChat:
		i.feedOpenAIChat(line)
	case StyleAnthropic:
		if strings.HasPrefix(line, "data: ") {
			i.lastContentType = contentTypeText
		}
	case StyleOpenAIResponses:
		if strings.HasPrefix(line, "data: ") {
			i.lastContentType = contentTypeText
		}
	}

	return chunk, nil
}

func (i *prematureCompletionInterceptor) ShouldContinue() bool {
	return i.shouldContinueReason() != ""
}

// shouldContinueReason returns why continuation is needed, or empty string if complete.
func (i *prematureCompletionInterceptor) shouldContinueReason() string {
	if i.isEmptyResponse() {
		return "empty response"
	}

	if i.lastContentType == contentTypeThinking {
		return "incomplete thinking"
	}

	content := i.accumulatedBytes.String()
	if i.hasMalformedThinking(content) {
		return "control token leakage"
	}

	if i.lastContentType == contentTypeText {
		content = strings.TrimSpace(i.accumulatedBytes.String())
		if strings.HasSuffix(content, ":") && len(content) > 1 {
			return "incomplete sentence"
		}
	}

	return ""
}

func (i *prematureCompletionInterceptor) Reset() {
	i.lastContentType = contentTypeEmpty
	i.emptyResponse = true
}

func (i *prematureCompletionInterceptor) GetOutput() string {
	return i.accumulatedBytes.String()
}

// isEmptyResponse returns true if no content was received at all.
func (i *prematureCompletionInterceptor) isEmptyResponse() bool {
	return i.emptyResponse && strings.TrimSpace(i.accumulatedBytes.String()) == ""
}

func (i *prematureCompletionInterceptor) feedOllama(line string) {
	var obj struct {
		Done     bool   `json:"done"`
		Response string `json:"response,omitempty"`
		Message  *struct {
			Content   string            `json:"content"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"message,omitempty"`
	}

	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return
	}
	if obj.Done {
		return
	}

	if obj.Message != nil && len(obj.Message.ToolCalls) > 0 {
		i.lastContentType = contentTypeToolCall
		return
	}

	content := obj.Response
	if obj.Message != nil && obj.Message.Content != "" {
		content = obj.Message.Content
	}
	if content != "" {
		i.emptyResponse = false
		i.accumulatedBytes.WriteString(content)
		i.lastContentType = i.ollamaContentType()
	}
}

func (i *prematureCompletionInterceptor) ollamaContentType() string {
	content := i.accumulatedBytes.String()
	lastOpen := strings.LastIndex(content, "</think>")
	lastClose := strings.LastIndex(content, "</th"+"ink"+"ing>")
	if lastOpen > lastClose {
		return contentTypeThinking
	}
	return contentTypeText
}

func (i *prematureCompletionInterceptor) hasMalformedThinking(content string) bool {
	// Detect leaked control tokens in output - model error state
	controlTokens := []string{
		"<" + "|im_start|>", "<" + "|im_end|>",
		"<" + "|user|>", "<" + "|assistant|>",
	}

	for _, token := range controlTokens {
		if strings.Contains(content, token) {
			return true
		}
	}
	return false
}

func (i *prematureCompletionInterceptor) feedOpenAIChat(line string) {
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" || !strings.HasPrefix(line, "data: ") {
		return
	}

	i.accumulatedBytes.WriteString(data)
	i.lastContentType = contentTypeText
}
