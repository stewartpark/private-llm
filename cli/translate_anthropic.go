package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// translateAnthropicRequest converts an Anthropic /v1/messages request body
// to an OpenAI /v1/chat/completions request body for vLLM.
// The model name is overridden with the configured model since Anthropic
// clients send model names like "claude-sonnet-4-20250514" which vLLM doesn't know.
func translateAnthropicRequest(body []byte) []byte {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return body
	}

	openaiReq := make(map[string]any)

	// Override model with configured model — Anthropic clients send Claude model
	// names (e.g. "claude-sonnet-4-20250514") which vLLM doesn't recognize.
	openaiReq["model"] = cfg.Model

	// Build messages array
	var messages []map[string]any

	// System message (top-level field in Anthropic)
	if system, ok := req["system"].(string); ok && system != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": system,
		})
	}
	// System can also be an array of content blocks
	if systemArr, ok := req["system"].([]any); ok && len(systemArr) > 0 {
		var systemText strings.Builder
		for _, block := range systemArr {
			if m, ok := block.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					if systemText.Len() > 0 {
						systemText.WriteString("\n")
					}
					systemText.WriteString(text)
				}
			}
		}
		if systemText.Len() > 0 {
			messages = append(messages, map[string]any{
				"role":    "system",
				"content": systemText.String(),
			})
		}
	}

	// Copy messages (role mapping is 1:1 for user/assistant)
	if msgArr, ok := req["messages"].([]any); ok {
		for _, m := range msgArr {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			openaiMsg := map[string]any{
				"role": msg["role"],
			}
			// Content can be string or array of content blocks
			switch content := msg["content"].(type) {
			case string:
				openaiMsg["content"] = content
			case []any:
				// Extract text from content blocks
				var text strings.Builder
				for _, block := range content {
					if b, ok := block.(map[string]any); ok {
						if t, ok := b["text"].(string); ok {
							text.WriteString(t)
						}
					}
				}
				openaiMsg["content"] = text.String()
			}
			messages = append(messages, openaiMsg)
		}
	}
	openaiReq["messages"] = messages

	// Direct mappings
	if maxTokens, ok := req["max_tokens"]; ok {
		openaiReq["max_tokens"] = maxTokens
	}
	if temp, ok := req["temperature"]; ok {
		openaiReq["temperature"] = temp
	}
	if topP, ok := req["top_p"]; ok {
		openaiReq["top_p"] = topP
	}
	if stop, ok := req["stop_sequences"]; ok {
		openaiReq["stop"] = stop
	}

	// Stream handling
	if stream, ok := req["stream"].(bool); ok {
		openaiReq["stream"] = stream
		if stream {
			openaiReq["stream_options"] = map[string]any{
				"include_usage": true,
			}
		}
	}

	result, err := json.Marshal(openaiReq)
	if err != nil {
		return body
	}
	return result
}

// anthropicTranslatingWriter wraps an http.ResponseWriter and translates
// OpenAI SSE lines to Anthropic SSE format on write. This allows the internal
// pipeline (token parser, interceptor) to see raw OpenAI SSE while the client
// receives properly formatted Anthropic SSE events.
type anthropicTranslatingWriter struct {
	w            http.ResponseWriter
	messageID    string
	model        string
	blockStarted bool
}

func newAnthropicTranslatingWriter(w http.ResponseWriter) *anthropicTranslatingWriter {
	return &anthropicTranslatingWriter{
		w:         w,
		messageID: "msg_vllm",
	}
}

func (tw *anthropicTranslatingWriter) Header() http.Header {
	return tw.w.Header()
}

func (tw *anthropicTranslatingWriter) WriteHeader(statusCode int) {
	tw.w.WriteHeader(statusCode)
}

// Write receives OpenAI SSE lines from the termination-aware writer and translates
// them to Anthropic SSE format before writing to the client.
func (tw *anthropicTranslatingWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")

	// Pass through empty lines and event-only lines
	if strings.TrimSpace(line) == "" {
		return tw.w.Write(p)
	}

	if !strings.HasPrefix(line, "data: ") {
		// Non-data lines (e.g. SSE comments) — pass through
		return tw.w.Write(p)
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		// Translate to message_stop
		tw.writeEvent("message_stop", `{"type":"message_stop"}`)
		return len(p), nil
	}

	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return tw.w.Write(p)
	}

	if chunk.Model != "" {
		tw.model = chunk.Model
	}
	if chunk.ID != "" {
		tw.messageID = chunk.ID
	}

	// First chunk with role: emit message_start + content_block_start
	if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Role != "" {
		startData, _ := json.Marshal(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    tw.messageID,
				"type":  "message",
				"role":  "assistant",
				"model": tw.model,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
		tw.writeEvent("message_start", string(startData))

		blockData, _ := json.Marshal(map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		tw.writeEvent("content_block_start", string(blockData))
		tw.blockStarted = true
		return len(p), nil
	}

	if len(chunk.Choices) > 0 {
		delta := chunk.Choices[0].Delta

		if !tw.blockStarted {
			blockData, _ := json.Marshal(map[string]any{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			tw.writeEvent("content_block_start", string(blockData))
			tw.blockStarted = true
		}

		if delta.Content != "" {
			deltaData, _ := json.Marshal(map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": delta.Content,
				},
			})
			tw.writeEvent("content_block_delta", string(deltaData))
		}

		if chunk.Choices[0].FinishReason != nil {
			stopData, _ := json.Marshal(map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			})
			tw.writeEvent("content_block_stop", string(stopData))

			stopReason := "end_turn"
			if *chunk.Choices[0].FinishReason == "length" {
				stopReason = "max_tokens"
			}

			outputTokens := int64(0)
			if chunk.Usage != nil {
				outputTokens = chunk.Usage.CompletionTokens
			}

			msgDelta, _ := json.Marshal(map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": stopReason,
				},
				"usage": map[string]any{
					"output_tokens": outputTokens,
				},
			})
			tw.writeEvent("message_delta", string(msgDelta))
		}
	}

	return len(p), nil
}

func (tw *anthropicTranslatingWriter) writeEvent(event, data string) {
	_, _ = fmt.Fprintf(tw.w, "event: %s\ndata: %s\n\n", event, data)
}

// Flush implements http.Flusher.
func (tw *anthropicTranslatingWriter) Flush() {
	if f, ok := tw.w.(http.Flusher); ok {
		f.Flush()
	}
}

// translateAnthropicNonStreamingResponse converts an OpenAI /v1/chat/completions
// JSON response to an Anthropic /v1/messages JSON response.
func translateAnthropicNonStreamingResponse(body []byte) []byte {
	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return body
	}

	content := ""
	stopReason := "end_turn"
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
		if resp.Choices[0].FinishReason == "length" {
			stopReason = "max_tokens"
		}
	}

	inputTokens := int64(0)
	outputTokens := int64(0)
	if resp.Usage != nil {
		inputTokens = resp.Usage.PromptTokens
		outputTokens = resp.Usage.CompletionTokens
	}

	anthResp := map[string]any{
		"id":           resp.ID,
		"type":         "message",
		"role":         "assistant",
		"model":        resp.Model,
		"stop_reason":  stopReason,
		"stop_sequence": nil,
		"content": []map[string]any{
			{
				"type": "text",
				"text": content,
			},
		},
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	result, err := json.Marshal(anthResp)
	if err != nil {
		return body
	}
	return result
}


