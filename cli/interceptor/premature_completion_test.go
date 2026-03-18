package interceptor

import (
	"fmt"
	"testing"
)

// feedChunks feeds a sequence of chunks to the interceptor.
func feedChunks(t *testing.T, i *prematureCompletionInterceptor, chunks [][]byte) {
	t.Helper()
	for idx, chunk := range chunks {
		if _, err := i.Feed(chunk, nil); err != nil {
			t.Fatalf("Feed chunk %d: %v", idx, err)
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

func TestOpenAIChat_NormalResponseNoContinuation(t *testing.T) {
	// OpenAI Chat accumulates raw JSON, so incomplete sentence detection
	// doesn't fire on content that ends with colon (the JSON ends with }).
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)
	chunks := [][]byte{
		[]byte(`data: {"choices":[{"delta":{"content":"Here are the steps:"}}]}`),
		[]byte("data: [DONE]"),
	}

	feedChunks(t, i, chunks)

	if i.ShouldContinue() {
		t.Fatalf("expected ShouldContinue=false for OpenAI Chat, reason: %q", i.shouldContinueReason())
	}
}

func TestOpenAIChat_EmptyResponse(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)
	i.Reset()
	feedChunks(t, i, [][]byte{
		[]byte("data: [DONE]"),
	})

	if !i.ShouldContinue() {
		t.Fatal("expected ShouldContinue=true for empty response after Reset")
	}
	if reason := i.shouldContinueReason(); reason != "empty response" {
		t.Fatalf("expected reason 'empty response', got %q", reason)
	}
}

func TestOpenAIChat_NormalResponse(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)
	chunks := [][]byte{
		[]byte(`data: {"choices":[{"delta":{"content":"Hello! Here is your answer."}}]}`),
		[]byte("data: [DONE]"),
	}

	feedChunks(t, i, chunks)

	if i.ShouldContinue() {
		t.Fatalf("expected ShouldContinue=false for normal response, reason: %q", i.shouldContinueReason())
	}
}

func TestHasMalformedThinking_AllControlTokens(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)

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
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)

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

func TestReset_PreservesAccumulatedBytes(t *testing.T) {
	i := newPrematureCompletionInterceptor(StyleOpenAIChat)
	feedChunks(t, i, [][]byte{
		[]byte(`data: {"choices":[{"delta":{"content":"thinking..."}}]}`),
	})

	output := i.GetOutput()
	if output == "" {
		t.Fatal("expected non-empty output before reset")
	}

	i.Reset()

	// After reset, accumulated bytes remain for GetOutput.
	output = i.GetOutput()
	if output == "" {
		t.Fatal("expected accumulated output preserved after reset")
	}
}
