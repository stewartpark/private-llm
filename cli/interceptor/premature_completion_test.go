package interceptor

import (
	"encoding/json"
	"fmt"
	"testing"
)

// ollamaChatChunk builds a streaming Ollama /api/chat JSON line.
func ollamaChatChunk(content string, done bool) []byte {
	obj := map[string]any{
		"done": done,
		"message": map[string]any{
			"role":    "assistant",
			"content": content,
		},
	}
	b, _ := json.Marshal(obj)
	return b
}

// ollamaGenerateChunk builds a streaming Ollama /api/generate JSON line.
func ollamaGenerateChunk(response string, done bool) []byte {
	obj := map[string]any{
		"done":     done,
		"response": response,
	}
	b, _ := json.Marshal(obj)
	return b
}

// feedChunks feeds a sequence of chunks to the interceptor.
func feedChunks(t *testing.T, i *prematureCompletionInterceptor, chunks [][]byte) {
	t.Helper()
	for idx, chunk := range chunks {
		if _, err := i.Feed(chunk, nil); err != nil {
			t.Fatalf("Feed chunk %d: %v", idx, err)
		}
	}
}

func TestOllamaContentType_OpenThinkBlock(t *testing.T) {
	// Model starts <think> but never closes it (stop token hit mid-thinking).
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("\nLet me reason about this...", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for open <think> block")
	}
	if reason := i.shouldContinueReason(); reason != "incomplete thinking" {
		t.Fatalf("expected reason 'incomplete thinking', got %q", reason)
	}
}

func TestOllamaContentType_ClosedThinkBlock(t *testing.T) {
	// Model properly closes thinking and produces text — should NOT trigger.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("reasoning", false),
		ollamaChatChunk("</think>", false),
		ollamaChatChunk("\n\nHere is the answer.", false),
		ollamaChatChunk("", true),
	})

	if i.ShouldContinue() {
		t.Fatalf("expected ShouldContinue=false for properly closed think block, reason: %q", i.shouldContinueReason())
	}
}

func TestOllamaContentType_StopTokenMidThinking(t *testing.T) {
	// Simulates: model generates <|im_end|> (stop token) mid-thinking.
	// Ollama swallows the stop token and sends done:true.
	// The <think> block is left open with no control tokens in content.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("\nI need to call a function", false),
		ollamaChatChunk("\nLet me use the search tool", false),
		// Model generates <|im_end|> here — Ollama treats it as stop signal.
		// No more content, just done:true.
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true when thinking block left open by stop token")
	}
	if reason := i.shouldContinueReason(); reason != "incomplete thinking" {
		t.Fatalf("expected reason 'incomplete thinking', got %q", reason)
	}
}

func TestOllamaContentType_ControlTokenInContent(t *testing.T) {
	// Control token appears as literal text in content (not swallowed as stop token).
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("Here is my answer ", false),
		ollamaChatChunk("<" + "|im_start|>", false),
		ollamaChatChunk("user", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for control token leakage in content")
	}
	if reason := i.shouldContinueReason(); reason != "control token leakage" {
		t.Fatalf("expected reason 'control token leakage', got %q", reason)
	}
}

func TestOllamaContentType_ControlTokenInOpenThinkBlock(t *testing.T) {
	// Control token inside an open think block — both issues present.
	// "incomplete thinking" should fire first (checked before hasMalformedThinking).
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("reasoning ", false),
		ollamaChatChunk("<"+"|im_start|>", false),
		ollamaChatChunk("user garbage", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true")
	}
	if reason := i.shouldContinueReason(); reason != "incomplete thinking" {
		t.Fatalf("expected reason 'incomplete thinking', got %q", reason)
	}
}

func TestOllamaContentType_GenerateAPI(t *testing.T) {
	// Same test with /api/generate format (uses "response" field).
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaGenerateChunk("<think>", false),
		ollamaGenerateChunk("\nthinking...", false),
		ollamaGenerateChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for open think block via /api/generate")
	}
}

func TestOllamaContentType_EmptyResponse(t *testing.T) {
	// emptyResponse is only set to true by Reset() (used for continuation attempts).
	// On first request, empty response detection requires Reset() to have been called.
	i := newPrematureCompletionInterceptor(StyleOllama)
	i.Reset() // Simulates continuation context where emptyResponse is armed.
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for empty response after Reset")
	}
	if reason := i.shouldContinueReason(); reason != "empty response" {
		t.Fatalf("expected reason 'empty response', got %q", reason)
	}
}

func TestOllamaContentType_NormalResponseNoThinking(t *testing.T) {
	// Plain response without thinking — should NOT trigger.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("Hello! Here is your answer.", false),
		ollamaChatChunk("", true),
	})

	if i.ShouldContinue() {
		t.Fatalf("expected ShouldContinue=false for normal response, reason: %q", i.shouldContinueReason())
	}
}

func TestOllamaContentType_MultipleThinkBlocks(t *testing.T) {
	// Multiple think blocks, all properly closed.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("first thought", false),
		ollamaChatChunk("</think>", false),
		ollamaChatChunk("\n\nPartial answer.\n\n", false),
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("second thought", false),
		ollamaChatChunk("</think>", false),
		ollamaChatChunk("\n\nFinal answer.", false),
		ollamaChatChunk("", true),
	})

	if i.ShouldContinue() {
		t.Fatalf("expected ShouldContinue=false, reason: %q", i.shouldContinueReason())
	}
}

func TestOllamaContentType_SecondThinkBlockOpen(t *testing.T) {
	// First think block closed, second one left open.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("first thought", false),
		ollamaChatChunk("</think>", false),
		ollamaChatChunk("\n\nPartial answer.\n\n", false),
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("second thought still going", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for second think block left open")
	}
	if reason := i.shouldContinueReason(); reason != "incomplete thinking" {
		t.Fatalf("expected reason 'incomplete thinking', got %q", reason)
	}
}

func TestOllamaContentType_IncompleteSentence(t *testing.T) {
	// Response ends with colon — incomplete sentence.
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("Here are the steps:", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for incomplete sentence")
	}
	if reason := i.shouldContinueReason(); reason != "incomplete sentence" {
		t.Fatalf("expected reason 'incomplete sentence', got %q", reason)
	}
}

func TestHasMalformedThinking_AllControlTokens(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOllama)

	tokens := []string{
		"<|im_start|>",
		"<|im_end|>",
		"<|user|>",
		"<|assistant|>",
	}

	for _, token := range tokens {
		t.Run(token, func(t *testing.T) {
			if !i.hasMalformedThinking(fmt.Sprintf("some text %s more text", token)) {
				t.Fatalf("expected hasMalformedThinking=true for %s", token)
			}
		})
	}
}

func TestHasMalformedThinking_NoControlTokens(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOllama)

	clean := []string{
		"normal response text",
		"<think>reasoning</think>\n\nAnswer",
		"code with <div> tags",
		"pipe | characters are fine",
	}

	for _, text := range clean {
		if i.hasMalformedThinking(text) {
			t.Fatalf("expected hasMalformedThinking=false for %q", text)
		}
	}
}

func TestOpenAIChat_ControlTokenDetection(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)

	// Simulate SSE chunks where control token appears in a single chunk's content.
	chunks := [][]byte{
		[]byte(`data: {"choices":[{"delta":{"content":"Hello "}}]}`),
		[]byte(fmt.Sprintf(`data: {"choices":[{"delta":{"content":"%s"}}]}`, "<|im_start|>")),
		[]byte(`data: {"choices":[{"delta":{"content":"user"}}]}`),
		[]byte("data: [DONE]"),
	}

	feedChunks(t, i, chunks)

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for control token in OpenAI Chat response")
	}
	if reason := i.shouldContinueReason(); reason != "control token leakage" {
		t.Fatalf("expected reason 'control token leakage', got %q", reason)
	}
}

func TestReset_PreservesAccumulatedBytes(t *testing.T) {
	// Verify Reset clears state but accumulated bytes persist (by design — used for continuation context).
	i := newPrematureCompletionInterceptor(StyleOllama)
	feedChunks(t, i, [][]byte{
		ollamaChatChunk("<think>", false),
		ollamaChatChunk("thinking...", false),
		ollamaChatChunk("", true),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true before reset")
	}

	i.Reset()

	// After reset, lastContentType is empty — but accumulated bytes remain for GetOutput.
	output := i.GetOutput()
	if output != "<think>thinking..." {
		t.Fatalf("expected accumulated output preserved after reset, got %q", output)
	}
}
