package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Content type categorization for last output
const (
	contentTypeText     = "text"
	contentTypeToolCall = "tool_call"
	contentTypeThinking = "thinking"
	contentTypeEmpty    = "empty"
)

// CompletionState tracks state of a streaming response for premature detection.
// Only tracks lastContentType and accumulated content — no counting heuristics.
type CompletionState struct {
	lastContentType    string
	accumulatedContent bytes.Buffer
}

// NewCompletionState creates a new tracking state for premature completion detection.
func NewCompletionState() *CompletionState {
	return &CompletionState{
		lastContentType: contentTypeEmpty,
	}
}

// ResetForContinuation resets lastContentType for a new continuation attempt,
// but preserves accumulatedContent so we can build the full assistant message.
func (s *CompletionState) ResetForContinuation() {
	s.lastContentType = contentTypeEmpty
}

// FeedOllama updates state from Ollama format (JSON lines).
func (s *CompletionState) FeedOllama(line string) {
	var obj struct {
		Done     bool   `json:"done"`
		Response string `json:"response"`
		Message  *struct {
			Content   string            `json:"content"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"message"`
	}

	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return
	}
	if obj.Done {
		return
	}

	// Track tool calls from message
	if obj.Message != nil && len(obj.Message.ToolCalls) > 0 {
		s.lastContentType = contentTypeToolCall
		return
	}

	// Track text content
	content := obj.Response
	if obj.Message != nil && obj.Message.Content != "" {
		content = obj.Message.Content
	}
	if content != "" {
		s.accumulatedContent.WriteString(content)
		s.lastContentType = contentTypeText
	}
}

// FeedOpenAIChat updates state from OpenAI Chat format (SSE).
func (s *CompletionState) FeedOpenAIChat(line string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return
	}

	var obj struct {
		Choices []struct {
			Delta struct {
				Content   string                                     `json:"content"`
				ToolCalls []struct{ Function struct{ Name string } } `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return
	}

	if len(obj.Choices) > 0 {
		d := obj.Choices[0].Delta
		if len(d.ToolCalls) > 0 {
			s.lastContentType = contentTypeToolCall
		}
		if d.Content != "" {
			s.accumulatedContent.WriteString(d.Content)
			s.lastContentType = contentTypeText
		}
	}
}

// FeedAnthropic updates state from Anthropic Messages format.
func (s *CompletionState) FeedAnthropic(line string, event string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")

	switch event {
	case "content_block_start":
		var obj struct {
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.ContentBlock.Type == "tool_use" {
				s.lastContentType = contentTypeToolCall
			}
		}

	case "content_block_delta":
		var obj struct {
			Delta struct {
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Delta.Thinking != "" {
				s.lastContentType = contentTypeThinking
			} else if obj.Delta.Text != "" {
				s.accumulatedContent.WriteString(obj.Delta.Text)
				s.lastContentType = contentTypeText
			} else if obj.Delta.PartialJSON != "" {
				// partial_json inside tool_use block — keep as tool_call
				s.lastContentType = contentTypeToolCall
			}
		}
	}
}

// FeedOpenAIResponses updates state from OpenAI Responses format.
func (s *CompletionState) FeedOpenAIResponses(line string, event string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}

	switch event {
	case "response.output_text.delta":
		data := strings.TrimPrefix(line, "data: ")
		var obj struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil && obj.Delta != "" {
			s.accumulatedContent.WriteString(obj.Delta)
			s.lastContentType = contentTypeText
		}

	case "response.function_call_arguments.delta":
		s.lastContentType = contentTypeToolCall

	case "response.reasoning_summary_text.delta":
		s.lastContentType = contentTypeThinking
	}
}

// IsPremature checks if this completion ended prematurely.
// Only thinking blocks are premature — tool calls and text are valid terminal states.
func (s *CompletionState) IsPremature() bool {
	return s.lastContentType == contentTypeThinking
}

// GetOutput returns the accumulated text output so far.
func (s *CompletionState) GetOutput() string {
	return s.accumulatedContent.String()
}

// FeedOllamaComplete updates state from a non-streaming Ollama response body.
func (s *CompletionState) FeedOllamaComplete(body []byte) {
	var obj struct {
		Message *struct {
			Content   string            `json:"content"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"message"`
		Response string `json:"response"`
	}
	if json.Unmarshal(body, &obj) != nil {
		return
	}
	if obj.Message != nil && len(obj.Message.ToolCalls) > 0 {
		s.lastContentType = contentTypeToolCall
	}
	content := obj.Response
	if obj.Message != nil && obj.Message.Content != "" {
		content = obj.Message.Content
	}
	if content != "" {
		s.accumulatedContent.WriteString(content)
		s.lastContentType = contentTypeText
	}
}

// FeedOpenAIChatComplete updates state from a non-streaming OpenAI Chat response body.
func (s *CompletionState) FeedOpenAIChatComplete(body []byte) {
	var obj struct {
		Choices []struct {
			Message struct {
				Content   string                                     `json:"content"`
				ToolCalls []struct{ Function struct{ Name string } } `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &obj) != nil || len(obj.Choices) == 0 {
		return
	}
	m := obj.Choices[0].Message
	if len(m.ToolCalls) > 0 {
		s.lastContentType = contentTypeToolCall
	}
	if m.Content != "" {
		s.accumulatedContent.WriteString(m.Content)
		s.lastContentType = contentTypeText
	}
}

// FeedAnthropicComplete updates state from a non-streaming Anthropic response body.
func (s *CompletionState) FeedAnthropicComplete(body []byte) {
	var obj struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(body, &obj) != nil {
		return
	}
	for _, block := range obj.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				s.accumulatedContent.WriteString(block.Text)
				s.lastContentType = contentTypeText
			}
		case "tool_use":
			s.lastContentType = contentTypeToolCall
		case "thinking":
			s.lastContentType = contentTypeThinking
		}
	}
}
