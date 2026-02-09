package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// Global cumulative token counters.
var (
	totalInputTokens  atomic.Int64
	totalOutputTokens atomic.Int64
)

// GetTokenCounts returns cumulative (input, output) token counts.
func GetTokenCounts() (int64, int64) {
	return totalInputTokens.Load(), totalOutputTokens.Load()
}

// apiStyle determines which parser to use for a given request path.
type apiStyle int

const (
	styleUnknown apiStyle = iota
	styleOllama           // /api/generate, /api/chat
	styleOpenAIChat       // /v1/chat/completions
	styleAnthropic        // /v1/messages
	styleOpenAIResponses  // /v1/responses
)

func detectAPIStyle(path string) apiStyle {
	switch {
	case strings.HasPrefix(path, "/api/generate"), strings.HasPrefix(path, "/api/chat"):
		return styleOllama
	case strings.HasPrefix(path, "/v1/chat/completions"):
		return styleOpenAIChat
	case strings.HasPrefix(path, "/v1/messages"):
		return styleAnthropic
	case strings.HasPrefix(path, "/v1/responses"):
		return styleOpenAIResponses
	default:
		return styleUnknown
	}
}

// tokenParser parses streaming response data to count tokens in real time.
// It buffers partial lines and processes complete ones.
type tokenParser struct {
	style  apiStyle
	buf    strings.Builder
	input  int64        // per-request input tokens
	output atomic.Int64 // per-request output tokens (atomic for cross-goroutine rate reads)

	// Tokens counted via streaming deltas only (for rate calculation).
	// Separate from output which may be corrected by authoritative API counts.
	streamed atomic.Int64

	// Timing for tok/sec calculation (atomic for cross-goroutine reads)
	upstreamStartNano atomic.Int64 // unix nano of when upstream request was sent
	firstOutputNano   atomic.Int64 // unix nano of first output token
	lastOutputNano    atomic.Int64 // unix nano of most recent output token

	// SSE state: tracks the last "event:" line for Anthropic/Responses
	lastEvent string
}

func newTokenParser(path string) *tokenParser {
	return &tokenParser{style: detectAPIStyle(path)}
}

// Feed processes a chunk of response data. Call this with each Read() result.
func (p *tokenParser) Feed(data []byte) {
	if p.style == styleUnknown {
		return
	}

	p.buf.Write(data)

	// Process complete lines
	for {
		content := p.buf.String()
		idx := strings.Index(content, "\n")
		if idx < 0 {
			break
		}
		line := content[:idx]
		p.buf.Reset()
		p.buf.WriteString(content[idx+1:])
		p.processLine(strings.TrimRight(line, "\r"))
	}
}

// Counts returns the per-request (input, output) token counts so far.
func (p *tokenParser) Counts() (int64, int64) {
	return p.input, p.output.Load()
}

// countOutput increments the output token counter, records timing, and
// updates the global cumulative counter. Thread-safe via atomics.
func (p *tokenParser) countOutput() {
	now := time.Now().UnixNano()
	p.firstOutputNano.CompareAndSwap(0, now)
	p.lastOutputNano.Store(now)
	p.output.Add(1)
	p.streamed.Add(1)
	totalOutputTokens.Add(1)
}

// InputRate returns the approximate input (prompt eval) tokens per second,
// estimated as input_tokens / TTFT. Only meaningful after the stream has started
// and input token count is known (usually at stream end).
func (p *tokenParser) InputRate() float64 {
	in := p.input
	if in == 0 {
		return 0
	}
	start := p.upstreamStartNano.Load()
	first := p.firstOutputNano.Load()
	if start == 0 || first == 0 || first <= start {
		return 0
	}
	ttft := float64(first-start) / 1e9
	return float64(in) / ttft
}

// LiveOutputRate returns the current output tokens per second, safe to call
// from any goroutine while streaming is in progress.
func (p *tokenParser) LiveOutputRate() float64 {
	n := p.streamed.Load()
	if n <= 1 {
		return 0
	}
	first := p.firstOutputNano.Load()
	last := p.lastOutputNano.Load()
	if first == 0 || last == 0 || last <= first {
		return 0
	}
	elapsed := float64(last-first) / 1e9
	return float64(n) / elapsed
}

func (p *tokenParser) processLine(line string) {
	switch p.style {
	case styleOllama:
		p.parseOllamaLine(line)
	case styleOpenAIChat:
		p.parseOpenAIChatLine(line)
	case styleAnthropic:
		p.parseAnthropicLine(line)
	case styleOpenAIResponses:
		p.parseOpenAIResponsesLine(line)
	}
}

// ── Ollama native (/api/generate, /api/chat) ─────────────────────
// Each line is a JSON object. Non-done lines with content = 1 output token.
// The done line has prompt_eval_count for input tokens.
func (p *tokenParser) parseOllamaLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var obj struct {
		Done             bool   `json:"done"`
		Response         string `json:"response"`          // /api/generate
		Message          *struct{ Content string } `json:"message"` // /api/chat
		PromptEvalCount  int64  `json:"prompt_eval_count"`
	}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return
	}

	if obj.Done {
		// Final line: grab input token count
		if obj.PromptEvalCount > 0 {
			p.input = obj.PromptEvalCount
			totalInputTokens.Add(obj.PromptEvalCount)
		}
		// Don't count output tokens from final line — already counted per-chunk
		return
	}

	// Each non-done line with content = 1 output token
	hasContent := obj.Response != ""
	if !hasContent && obj.Message != nil {
		hasContent = obj.Message.Content != ""
	}
	if hasContent {
		p.countOutput()
	}
}

// ── OpenAI Chat (/v1/chat/completions) ───────────────────────────
// SSE format: "data: {...}" lines. Each with delta.content = 1 output token.
func (p *tokenParser) parseOpenAIChatLine(line string) {
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
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return
	}

	// Count content + reasoning deltas as output tokens
	if len(obj.Choices) > 0 {
		d := obj.Choices[0].Delta
		if d.Content != "" || d.ReasoningContent != "" {
			p.countOutput()
		}
	}

	// Grab input tokens from usage if present (usually in final chunk)
	if obj.Usage != nil && obj.Usage.PromptTokens > 0 && p.input == 0 {
		p.input = obj.Usage.PromptTokens
		totalInputTokens.Add(obj.Usage.PromptTokens)
	}
}

// ── Anthropic Messages (/v1/messages) ────────────────────────────
// SSE format: "event: <type>\ndata: {...}" pairs.
// message_start has input_tokens; each content_block_delta = 1 output token.
func (p *tokenParser) parseAnthropicLine(line string) {
	if strings.HasPrefix(line, "event: ") {
		p.lastEvent = strings.TrimPrefix(line, "event: ")
		return
	}

	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")

	switch p.lastEvent {
	case "message_start":
		var obj struct {
			Message struct {
				Usage struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Message.Usage.InputTokens > 0 {
				p.input = obj.Message.Usage.InputTokens
				totalInputTokens.Add(obj.Message.Usage.InputTokens)
			}
		}

	case "content_block_delta":
		// Each delta = 1 output token (text, thinking, or tool use)
		var obj struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Delta.Text != "" || obj.Delta.Thinking != "" || obj.Delta.PartialJSON != "" {
				p.countOutput()
			}
		}

	case "message_delta":
		// Authoritative output token count from API
		var obj struct {
			Usage struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Usage.OutputTokens > 0 {
				counted := p.output.Load()
				diff := obj.Usage.OutputTokens - counted
				if diff != 0 {
					p.output.Add(diff)
					totalOutputTokens.Add(diff)
				}
			}
		}
	}
}

// ── OpenAI Responses (/v1/responses) ─────────────────────────────
// SSE format: "event: <type>\ndata: {...}" pairs.
// response.output_text.delta events = 1 output token each.
// response.completed has usage.input_tokens.
func (p *tokenParser) parseOpenAIResponsesLine(line string) {
	if strings.HasPrefix(line, "event: ") {
		p.lastEvent = strings.TrimPrefix(line, "event: ")
		return
	}

	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")

	switch p.lastEvent {
	case "response.output_text.delta",
		"response.reasoning_summary_text.delta",
		"response.function_call_arguments.delta":
		// Each delta = 1 output token (text, thinking, or tool call)
		var obj struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Delta != "" {
				p.countOutput()
			}
		}

	case "response.completed":
		// Use authoritative token counts from the completed response
		var obj struct {
			Response struct {
				Usage struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			if obj.Response.Usage.InputTokens > 0 && p.input == 0 {
				p.input = obj.Response.Usage.InputTokens
				totalInputTokens.Add(obj.Response.Usage.InputTokens)
			}
			// Correct output count with authoritative value from API
			if obj.Response.Usage.OutputTokens > 0 {
				counted := p.output.Load()
				diff := obj.Response.Usage.OutputTokens - counted
				if diff != 0 {
					p.output.Add(diff)
					totalOutputTokens.Add(diff)
				}
			}
		}
	}
}
