package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stewartpark/private-llm/cli/common"
)

func TestIsGenerationEndpoint(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Ollama native endpoints
		{"/api/generate", true},
		{"/api/chat", true},

		// OpenAI-compatible endpoints
		{"/v1/chat/completions", true},
		{"/v1/responses", true},

		// Anthropic endpoint
		{"/v1/messages", true},

		// Non-generation endpoints (should go through pass-through)
		{"/api/tags", false},
		{"/api/pull", false},
		{"/api/push", false},
		{"/api/show", false},
		{"/api/copy", false},
		{"/api/delete", false},
		{"/api/blobs/sha256:abc123", false},
		{"/api/embed", false},
		{"/api/embeddings", false},
		{"/v1/models", false},

		// Paths with query strings (should not match)
		{"/api/tags?format=json", false},
		{"/api/pull?name=llama2", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isGenerationEndpoint(tt.path)
			if result != tt.expected {
				t.Errorf("isGenerationEndpoint(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestMergeOllamaResponse_Chat(t *testing.T) {
	first := []byte(`{"message":{"content":"Hello "},"done":false}`)
	second := []byte(`{"message":{"content":"world!"},"done":true}`)

	merged := mergeOllamaResponse(first, second)

	got := string(merged)
	expectedContent := `"content":"Hello world!"`
	if !containsSubstring(got, expectedContent) {
		t.Errorf("mergeOllamaResponse chat: got %q, expected content to contain %q", got, expectedContent)
	}
}

func TestMergeOllamaResponse_Generate(t *testing.T) {
	first := []byte(`{"response":"Hello ","done":false}`)
	second := []byte(`{"response":"world!","done":true}`)

	merged := mergeOllamaResponse(first, second)

	got := string(merged)
	expectedResponse := `"response":"Hello world!"`
	if !containsSubstring(got, expectedResponse) {
		t.Errorf("mergeOllamaResponse generate: got %q, expected response to contain %q", got, expectedResponse)
	}
}

func TestMergeOllamaResponse_ToolCalls(t *testing.T) {
	first := []byte(`{"message":{"content":"I'll use a tool","tool_calls":[{"id":"1","function":{"name":"search"}}]}}`)
	second := []byte(`{"message":{"tool_calls":[{"id":"2","function":{"name":"calculate"}}]}}`)

	merged := mergeOllamaResponse(first, second)

	got := string(merged)
	// Should have both tool calls
	if !containsSubstring(got, `"id":"1"`) || !containsSubstring(got, `"id":"2"`) {
		t.Errorf("mergeOllamaResponse tool_calls: expected both calls in %q", got)
	}
}

func TestMergeOpenAIChatResponse_Basic(t *testing.T) {
	first := []byte(`{"choices":[{"message":{"content":"First "}}]}`)
	second := []byte(`{"choices":[{"message":{"content":"part"}}]}`)

	merged := mergeOpenAIChatResponse(first, second)

	got := string(merged)
	expectedContent := `"content":"First part"`
	if !containsSubstring(got, expectedContent) {
		t.Errorf("mergeOpenAIChatResponse: got %q, expected %q", got, expectedContent)
	}
}

func TestMergeAnthropicResponse_MultipleBlocks(t *testing.T) {
	first := []byte(`{"content":[{"type":"text","text":"Hello "}]}`)
	second := []byte(`{"content":[{"type":"text","text":"world"}]}`)

	merged := mergeAnthropicResponse(first, second)

	got := string(merged)
	if !containsSubstring(got, `"text":"Hello "`) || !containsSubstring(got, `"text":"world"`) {
		t.Errorf("mergeAnthropicResponse: expected both content blocks in %q", got)
	}
}

// containsSubstring checks if str contains substr
func containsSubstring(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || len(substr) == 0 || findSubstring(str, substr))
}

func findSubstring(str, substr string) bool {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func newTestTerminationWriter(style common.APIStyle) *terminationAwareWriter {
	return &terminationAwareWriter{
		w:          &testResponseWriter{},
		style:      style,
		feedCh:     make(chan []byte, 10),
		feedSync:   make(chan struct{}, 10),
		isCont:     false,
		hasFlusher: false,
	}
}

type testResponseWriter struct {
	data []byte
	code int
}

func (t *testResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (t *testResponseWriter) Write(b []byte) (int, error) {
	t.data = append(t.data, b...)
	return len(b), nil
}

func (t *testResponseWriter) WriteHeader(statusCode int) {
	t.code = statusCode
}

func TestIsTerminationLine_Ollama(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{`{"done":true,"message":{}}`, true},
		{`{"done": false,"message":{}}`, false},
		{`{"done":false,"message":{}}`, false},
		{`{"message":{"content":"hello"}}`, false},
		{`{"done":true}`, true},
	}

	for _, tt := range tests {
		tw := newTestTerminationWriter(common.StyleOllama)
		result := tw.isTerminationLine(tt.line)
		if result != tt.expected {
			t.Errorf("isTerminationLine(Ollama, %q) = %v, want %v", tt.line, result, tt.expected)
		}
	}
}

func TestIsTerminationLine_OpenAIChat(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"data: [DONE]", true},
		{"data: [DONE]  ", true},
		{"event: message", false},
	}

	for _, tt := range tests {
		tw := newTestTerminationWriter(common.StyleOpenAIChat)
		result := tw.isTerminationLine(tt.line)
		if result != tt.expected {
			t.Errorf("isTerminationLine(OpenAI, %q) = %v, want %v", tt.line, result, tt.expected)
		}
	}
}

func TestIsTerminationLine_Anthropic(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleAnthropic)

	// Event line alone should return true and set state
	if !tw.isTerminationLine("event: message_stop") {
		t.Error("expected event: message_stop to be termination")
	}
	if tw.lastEvent != "message_stop" {
		t.Errorf("expected lastEvent=message_stop, got %q", tw.lastEvent)
	}

	// Data line after event should also return true
	if !tw.isTerminationLine("data: {\"stop_reason\": \"end_turn\"}") {
		t.Error("expected data line after message_stop to be termination")
	}

	// Other events should not terminate
	tw.lastEvent = ""
	if tw.isTerminationLine("event: content_block_start") {
		t.Error("content_block_start should not be termination")
	}
}

func TestIsTerminationLine_OpenAIResponses(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleOpenAIResponses)

	if !tw.isTerminationLine("event: response.completed") {
		t.Error("expected response.completed event to be termination")
	}
	if tw.lastEvent != "response.completed" {
		t.Errorf("expected lastEvent=response.completed, got %q", tw.lastEvent)
	}

	if !tw.isTerminationLine("data: {\"output\": []}") {
		t.Error("expected data line after response.completed to be termination")
	}
}

func TestIsEmptyOrEventOnly(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"", true},
		{"   ", true},
		{"event: message_start", true},
		{"event: message_delta", true},
		{"data: {}", false},
	}

	tw := newTestTerminationWriter(common.StyleOllama)
	for _, tt := range tests {
		result := tw.isEmptyOrEventOnly(tt.line)
		if result != tt.expected {
			t.Errorf("isEmptyOrEventOnly(%q) = %v, want %v", tt.line, result, tt.expected)
		}
	}
}

func TestMergeOllamaResponse_InvalidJSON(t *testing.T) {
	first := []byte(`{"valid": true}`)
	second := []byte(`{invalid json}`)

	merged := mergeOllamaResponse(first, second)
	if string(merged) != string(first) {
		t.Error("mergeOllamaResponse should return first on invalid JSON")
	}
}

func TestMergeOllamaResponse_MissingFields(t *testing.T) {
	first := []byte(`{"message":{}}`) // no content field
	second := []byte(`{"message":{"content":"world"}}`)

	merged := mergeOllamaResponse(first, second)
	got := string(merged)
	if !containsSubstring(got, `"content":"world"`) {
		t.Errorf("expected merged content in %q", got)
	}
}

func TestMergeOpenAIChatResponse_InvalidJSON(t *testing.T) {
	first := []byte(`{"choices":[]}`)
	second := []byte(`not json`)

	merged := mergeOpenAIChatResponse(first, second)
	if string(merged) != string(first) {
		t.Error("mergeOpenAIChatResponse should return first on invalid JSON")
	}
}

func TestMergeOpenAIChatResponse_EmptyChoices(t *testing.T) {
	first := []byte(`{"choices":[]}`)
	second := []byte(`{"choices":[{"message":{"content":"test"}}]}`)

	merged := mergeOpenAIChatResponse(first, second)
	if string(merged) != string(first) {
		t.Error("mergeOpenAIChatResponse should return first when choices empty")
	}
}

func TestMergeAnthropicResponse_InvalidJSON(t *testing.T) {
	first := []byte(`{"content":[]}`)
	second := []byte(`invalid`)

	merged := mergeAnthropicResponse(first, second)
	if string(merged) != string(first) {
		t.Error("mergeAnthropicResponse should return first on invalid JSON")
	}
}

func TestMergeAnthropicResponse_NonSliceContent(t *testing.T) {
	first := []byte(`{"content":"not an array"}`)
	second := []byte(`{"content":[{"type":"text"}]}`)

	merged := mergeAnthropicResponse(first, second)
	if string(merged) != string(first) {
		t.Error("mergeAnthropicResponse should return first when content not a slice")
	}
}

func TestBuildContinuationRequest_OllamaChat(t *testing.T) {
	original := []byte(`{"model":"qwen3.5","messages":[{"role":"user","content":"Hello"}]}`)
	output := "Nice to meet you"

	result := buildContinuationRequest(original, output, StyleOllama)
	if result == nil {
		t.Fatal("expected non-nil continuation request")
	}

	var req map[string]any
	if err := json.Unmarshal(result, &req); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	messages, ok := req["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	// Check assistant message was appended
	assistant, ok := messages[1].(map[string]any)
	if !ok || assistant["role"] != "assistant" || assistant["content"] != output {
		t.Errorf("unexpected assistant message: %v", messages[1])
	}

	// Check Continue. user message was appended
	user, ok := messages[2].(map[string]any)
	if !ok || user["role"] != "user" || user["content"] != "Continue." {
		t.Errorf("unexpected continue message: %v", messages[2])
	}
}

func TestBuildContinuationRequest_OpenAIChat(t *testing.T) {
	original := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}]}`)
	output := "Hello there"

	result := buildContinuationRequest(original, output, StyleOpenAIChat)
	if result == nil {
		t.Fatal("expected non-nil continuation request")
	}

	var req map[string]any
	if err := json.Unmarshal(result, &req); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	messages, ok := req["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %v", messages)
	}
}

func TestBuildContinuationRequest_Anthropic(t *testing.T) {
	original := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"Test"}]}`)
	output := "Testing"

	result := buildContinuationRequest(original, output, StyleAnthropic)
	if result == nil {
		t.Fatal("expected non-nil continuation request")
	}

	var req map[string]any
	if err := json.Unmarshal(result, &req); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	messages, ok := req["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
}

func TestBuildContinuationRequest_GenerateAPINotSupported(t *testing.T) {
	// /api/generate doesn't have messages array — should return nil
	original := []byte(`{"model":"llama2","prompt":"Hello"}`)
	output := "World"

	result := buildContinuationRequest(original, output, StyleOllama)
	if result != nil {
		t.Error("expected nil for /api/generate (no messages field)")
	}
}

func TestBuildContinuationRequest_InvalidJSON(t *testing.T) {
	original := []byte(`{not valid json}`)
	output := "test"

	result := buildContinuationRequest(original, output, StyleOllama)
	if result != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestBuildContinuationRequest_ResponsesAPINotSupported(t *testing.T) {
	// OpenAI Responses API not supported yet
	original := []byte(`{"input":"test"}`)
	output := "result"

	result := buildContinuationRequest(original, output, StyleOpenAIResponses)
	if result != nil {
		t.Error("expected nil for StyleOpenAIResponses (not supported)")
	}
}

func TestIsPreambleLine_OpenAIChat(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleOpenAIChat)
	tw.isCont = true // enable continuation mode

	// Role-setting chunk should be stripped
	if !tw.isPreambleLine(`data: {"choices":[{"delta":{"role":"assistant"}}]}`) {
		t.Error("expected role-setting chunk to be preamble")
	}

	// Content chunks should not be stripped after role
	if tw.isPreambleLine(`data: {"choices":[{"delta":{"content":"hello"}}]}`) {
		t.Error("content chunk should not be preamble after seenContent")
	}
}

func TestIsPreambleLine_Anthropic(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleAnthropic)
	tw.isCont = true

	// message_start event should be stripped
	if !tw.isPreambleLine("event: message_start") {
		t.Error("expected message_start to be preamble")
	}
	if tw.lastEvent != "message_start" {
		t.Errorf("expected lastEvent=message_start, got %q", tw.lastEvent)
	}

	// Data line after message_start should also be stripped
	if !tw.isPreambleLine("data: {\"id\":\"msg_123\"}") {
		t.Error("expected data after message_start to be preamble")
	}

	// content_block_start should be stripped
	tw.lastEvent = ""
	if !tw.isPreambleLine("event: content_block_start") {
		t.Error("expected content_block_start to be preamble")
	}
}

func TestIsPreambleLine_NotContinuation(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleOpenAIChat)
	tw.isCont = false // not a continuation

	// Should never strip preamble in non-continuation mode
	if tw.isPreambleLine(`data: {"choices":[{"delta":{"role":"assistant"}}]}`) {
		t.Error("should not strip preamble when isCont=false")
	}
}

func TestIsPreambleLine_OpenAIResponses(t *testing.T) {
	tw := newTestTerminationWriter(common.StyleOpenAIResponses)
	tw.isCont = true

	// response.created should be stripped
	if !tw.isPreambleLine("event: response.created") {
		t.Error("expected response.created to be preamble")
	}

	// Data line after event should be stripped
	if !tw.isPreambleLine("data: {\"id\":\"resp_123\"}") {
		t.Error("expected data after response.created to be preamble")
	}

	// response.output_item.added should also be stripped
	tw.lastEvent = ""
	if !tw.isPreambleLine("event: response.output_item.added") {
		t.Error("expected response.output_item.added to be preamble")
	}
}

func TestIsPreambleLine_OllamaNoPreamble(t *testing.T) {
	// Ollama doesn't have special preamble lines
	tw := newTestTerminationWriter(common.StyleOllama)
	tw.isCont = true

	if tw.isPreambleLine(`{"message":{"content":"hello"}}`) {
		t.Error("Ollama should not have preamble lines")
	}
}
