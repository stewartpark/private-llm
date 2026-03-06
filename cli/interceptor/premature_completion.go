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
}

func newPrematureCompletionInterceptor(style APIStyle) *prematureCompletionInterceptor {
	return &prematureCompletionInterceptor{
		style:           style,
		lastContentType: contentTypeEmpty,
	}
}

func (i *prematureCompletionInterceptor) Feed(chunk []byte) ([]byte, error) {
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
	if i.lastContentType == contentTypeThinking {
		return true
	}

	if i.lastContentType == contentTypeText {
		content := strings.TrimSpace(i.accumulatedBytes.String())
		if strings.HasSuffix(content, ":") && len(content) > 1 {
			return true
		}
	}

	return false
}

func (i *prematureCompletionInterceptor) Reset() {
	i.lastContentType = contentTypeEmpty
}

func (i *prematureCompletionInterceptor) GetOutput() string {
	return i.accumulatedBytes.String()
}

func (i *prematureCompletionInterceptor) feedOllama(line string) {
	var obj struct {
		Done bool `json:"done"`
		Response string `json:"response,omitempty"`
		Message *struct {
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
		i.accumulatedBytes.WriteString(content)
		i.lastContentType = i.ollamaContentType()
	}
}

func (i *prematureCompletionInterceptor) ollamaContentType() string {
	content := i.accumulatedBytes.String()
	lastOpen := strings.LastIndex(content, "<think>")
	lastClose := strings.LastIndex(content, "</think>")
	if lastOpen > lastClose {
		return contentTypeThinking
	}
	return contentTypeText
}

func (i *prematureCompletionInterceptor) feedOpenAIChat(line string) {
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" || !strings.HasPrefix(line, "data: ") {
		return
	}
	
	i.accumulatedBytes.WriteString(data)
	i.lastContentType = contentTypeText
}
