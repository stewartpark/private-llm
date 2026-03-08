package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stewartpark/private-llm/cli/interceptor"
	"github.com/stewartpark/private-llm/cli/tui"
)

var (
	vmIP            string
	rotatedOnce     bool
	proxyReady      atomic.Bool
	lastRequestTime time.Time
	lastRequestMu   sync.RWMutex
	assigner        *backendAssigner // initialized in serve with cfg.NumInstances
)

// Resettable channel gate: closed channel = gate open (requests pass),
// unclosed channel = gate closed (requests block at waitReady).
var (
	readyMu sync.Mutex
	readyCh = make(chan struct{}) // starts blocking (gate closed)
)

func waitReady(ctx context.Context) error {
	readyMu.Lock()
	ch := readyCh
	readyMu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openGate() {
	readyMu.Lock()
	defer readyMu.Unlock()
	select {
	case <-readyCh: // already open
	default:
		close(readyCh)
	}
}

func closeGate() {
	readyMu.Lock()
	defer readyMu.Unlock()
	select {
	case <-readyCh: // open → replace with fresh blocking channel
		readyCh = make(chan struct{})
	default: // already blocking
	}
}

// getCachedVMIP returns the cached VM IP address.
func getCachedVMIP() string {
	return vmIP
}

// IsProxyReady returns true if the proxy has successfully connected to the VM.
func IsProxyReady() bool {
	return proxyReady.Load()
}

// ClearProxyReady marks the proxy as not ready (e.g. when the VM stops externally).
func ClearProxyReady() {
	proxyReady.Store(false)
}

// GetLastRequestTime returns the time of the last completed proxy request.
func GetLastRequestTime() time.Time {
	lastRequestMu.RLock()
	defer lastRequestMu.RUnlock()
	return lastRequestTime
}

// resetProxyState clears cached proxy state so doSetup re-discovers the VM.
// Must be called while holding ops.mu.
func resetProxyState() {
	vmIP = ""
	proxyReady.Store(false)
	closeGate()
	rotatedOnce = false
	invalidateTLSConfig()
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	reqStart := time.Now()
	ctx := r.Context()

	// Lazily trigger VM boot on first request (no-op if already running).
	ops.EnsureSetup()

	// Block until the proxy is ready (gate open). If the client disconnects
	// while waiting, return 503 immediately instead of hanging.
	if err := waitReady(ctx); err != nil {
		http.Error(w, "proxy not ready: client disconnected", http.StatusServiceUnavailable)
		return
	}

	ip := getCachedVMIP()

	// Load mTLS config
	tlsCfg, internalToken, err := getTLSConfig(ctx)
	if err != nil {
		log.Printf("[proxy] getTLSConfig failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to load certs: %v", err), http.StatusInternalServerError)
		return
	}

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	// Fan-out /api/ps across all backends and merge results.
	if r.URL.Path == "/api/ps" {
		fanOutPS(ctx, w, client, ip, internalToken)
		return
	}

	// Buffer request body so we can peek at "model" and "stream", and replay on retries.
	var reqBody []byte
	var modelName string
	isStreaming := true // default: streaming (Ollama defaults to true)
	if r.Body != nil {
		reqBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
			return
		}
		var peek chatRequest
		if json.Unmarshal(reqBody, &peek) == nil {
			if peek.Model != "" {
				modelName = peek.Model
			}
			if peek.Stream != nil && !*peek.Stream {
				isStreaming = false
			}
		}
	}

	// Session-aware backend selection for KV cache affinity
	backend := assigner.Acquire(reqBody)
	defer assigner.Release(backend)

	// Path-based routing: always use /backend/N prefix for deterministic routing
	var endpoint string
	endpoint = fmt.Sprintf("https://%s:8080/backend/%d%s", ip, backend, r.URL.Path)
	if r.URL.RawQuery != "" {
		endpoint += "?" + r.URL.RawQuery
	}

	// Retry loop for 502s (Caddy up, Ollama not ready) and connection errors.
	var resp *http.Response
	var upstreamStart time.Time
	maxRetries := 12
	for attempt := range maxRetries {
		proxyReq, err := http.NewRequestWithContext(ctx, r.Method, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
			return
		}

		// Copy headers except Authorization
		for key, values := range r.Header {
			if key == "Authorization" {
				continue
			}
			for _, value := range values {
				proxyReq.Header.Add(key, value)
			}
		}
		proxyReq.Header.Set("Authorization", "Bearer "+internalToken)
		proxyReq.Host = "private-llm-server"

		upstreamStart = time.Now()
		resp, err = client.Do(proxyReq)
		if err != nil {
			log.Printf("[proxy] request failed (attempt %d/%d): %v", attempt+1, maxRetries, err)
			// On first failure, signal ops loop to recover and wait for gate
			if attempt == 0 {
				ops.RequestRecovery()
				if waitErr := waitReady(ctx); waitErr != nil {
					http.Error(w, "proxy not ready: client disconnected", http.StatusServiceUnavailable)
					return
				}
				// Re-read state after recovery
				ip = getCachedVMIP()
				endpoint = fmt.Sprintf("https://%s:8080/backend/%d%s", ip, backend, r.URL.Path)
				if r.URL.RawQuery != "" {
					endpoint += "?" + r.URL.RawQuery
				}
				tlsCfg, internalToken, err = getTLSConfig(ctx)
				if err != nil {
					http.Error(w, fmt.Sprintf("Failed to refresh certs: %v", err), http.StatusInternalServerError)
					return
				}
				client.Transport = &http.Transport{TLSClientConfig: tlsCfg}
			}
			if attempt < maxRetries-1 {
				time.Sleep(5 * time.Second)
				continue
			}
			http.Error(w, fmt.Sprintf("Failed to reach VM: %v", err), http.StatusBadGateway)
			return
		}

		if resp.StatusCode == http.StatusBadGateway {
			_ = resp.Body.Close()
			log.Printf("[proxy] 502 (attempt %d/%d), Ollama not ready, retrying...", attempt+1, maxRetries)
			if attempt < maxRetries-1 {
				time.Sleep(5 * time.Second)
				continue
			}
			http.Error(w, "Ollama service not ready after retries", http.StatusBadGateway)
			return
		}

		break
	}
	defer resp.Body.Close() //nolint:errcheck

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Token parser — counts tokens from each streamed chunk in real time.
	// Runs async so parsing never blocks the proxy write path.
	tp := newTokenParser(r.URL.Path)
	tp.upstreamStartNano.Store(upstreamStart.UnixNano())

	// Setup log callback for interceptors
	logCb := func(msg string) {
		if tuiProg != nil {
			tuiProg.Send(tui.LogBatch{Lines: []string{msg}})
		}
	}

	// Request handler for response processing (premature detection, tool call extraction)
	handler := interceptor.NewRequestHandler(tp.style, interceptor.WithLogCallback(logCb))

	feedCh := make(chan []byte, 64)
	feedSync := make(chan struct{}) // send to request sync, receive to confirm
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		for {
			select {
			case chunk, ok := <-feedCh:
				if !ok {
					return
				}
				tp.Feed(chunk)
				_, _ = handler.Feed(chunk)
			case <-feedSync:
				// Drain any remaining items in feedCh before confirming
				for {
					select {
					case chunk := <-feedCh:
						tp.Feed(chunk)
						_, _ = handler.Feed(chunk)
					default:
						goto drained
					}
				}
			drained:
				feedSync <- struct{}{} // confirm sync
			}
		}
	}()

	// Send live tok/sec updates to TUI during streaming
	rateDone := make(chan struct{})
	if tuiProg != nil {
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-rateDone:
					return
				case <-ticker.C:
					if rate := tp.LiveOutputRate(); rate > 0 {
						tuiProg.Send(tui.StreamingRate{OutputTokPerSec: rate})
					}
				}
			}
		}()
	}

	const maxContinuations = 3

	if isStreaming {
		// Termination-aware streaming: stream content to client immediately, but hold
		// back termination signals. After upstream ends, check if premature. If so,
		// discard held termination and make a continuation request. Otherwise release it.
		flusher, hasFlusher := w.(http.Flusher)

		for attempt := range maxContinuations + 1 {
			currentResp := resp
			if attempt > 0 {
				currentResp = makeContinuationRequest(ctx, r, client, endpoint, reqBody, handler.GetOutput(), tp.style, internalToken)
				if currentResp == nil {
					break
				}
			}

			tw := newTerminationAwareWriter(w, flusher, hasFlusher, tp.style, feedCh, feedSync, attempt > 0)
			tw.StreamResponse(currentResp.Body)
			_ = currentResp.Body.Close()

			// Drain feedCh processing before checking premature state
			tw.WaitFed()

			if attempt < maxContinuations && handler.ShouldContinue() {
				reason := handler.ShouldContinueReason()
				tw.DiscardTermination()
				handler.Reset()
				log.Printf("[proxy] premature completion detected (continuation #%d, reason: %s)", attempt+1, reason)
				if tuiProg != nil {
					tuiProg.Send(tui.LogBatch{Lines: []string{fmt.Sprintf("[intercept] Continuation #%d: %s", attempt+1, reason)}})
				}
				continue
			}

			tw.ReleaseTermination(flusher, hasFlusher)
			break
		}
	} else {
		// Non-streaming: buffer entire response, check premature, merge if needed.
		var finalBody []byte

		for attempt := range maxContinuations + 1 {
			currentResp := resp
			if attempt > 0 {
				currentResp = makeContinuationRequest(ctx, r, client, endpoint, reqBody, handler.GetOutput(), tp.style, internalToken)
				if currentResp == nil {
					break
				}
			}

			body, _ := io.ReadAll(currentResp.Body)
			_ = currentResp.Body.Close()

			// Feed to token parser and request handler
			feedCh <- body
			_, _ = handler.Feed(body)

			if finalBody == nil {
				finalBody = body
			} else {
				finalBody = mergeNonStreamingResponse(finalBody, body, tp.style)
			}

			if attempt < maxContinuations && handler.ShouldContinue() {
				reason := handler.ShouldContinueReason()
				handler.Reset()
				log.Printf("[proxy] premature completion detected (continuation #%d, reason: %s)", attempt+1, reason)
				if tuiProg != nil {
					tuiProg.Send(tui.LogBatch{Lines: []string{fmt.Sprintf("[intercept] Continuation #%d: %s", attempt+1, reason)}})
				}
				continue
			}
			break
		}

		if finalBody != nil {
			_, _ = w.Write(finalBody)
		}
	}

	close(feedCh)
	<-feedDone // wait for parser to drain before reading final counts
	close(rateDone)

	// Update last request time for idle tracking
	lastRequestMu.Lock()
	lastRequestTime = time.Now()
	lastRequestMu.Unlock()

	// Send request event to TUI (after streaming, so token counts are final)
	inputTok, outputTok := tp.Counts()
	finalRate := tp.LiveOutputRate()
	inputRate := tp.InputRate()
	if tuiProg != nil {
		tuiProg.SendEvent(tui.RequestEvent{
			Timestamp:       reqStart,
			Method:          r.Method,
			Path:            r.URL.Path,
			ModelName:       modelName,
			Status:          resp.StatusCode,
			Duration:        time.Since(reqStart),
			Encrypted:       true,
			InputTokens:     inputTok,
			OutputTokens:    outputTok,
			InputTokPerSec:  inputRate,
			OutputTokPerSec: finalRate,
		})
	}
}

// fanOutPS queries /api/ps on all backends concurrently and merges the results.
func fanOutPS(ctx context.Context, w http.ResponseWriter, client *http.Client, ip, token string) {
	type psResponse struct {
		Models []json.RawMessage `json:"models"`
	}

	type result struct {
		models []json.RawMessage
		err    error
	}

	n := assigner.numInstances
	results := make([]result, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func(backend int) {
			defer wg.Done()
			endpoint := fmt.Sprintf("https://%s:8080/backend/%d/api/ps", ip, backend+1)
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
			if err != nil {
				results[backend].err = err
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Host = "private-llm-server"

			resp, err := client.Do(req)
			if err != nil {
				results[backend].err = err
				return
			}
			defer func() { _ = resp.Body.Close() }()

			var ps psResponse
			if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
				results[backend].err = err
				return
			}
			results[backend].models = ps.Models
		}(i)
	}
	wg.Wait()

	var merged []json.RawMessage
	for i, r := range results {
		if r.err != nil {
			log.Printf("[proxy] /api/ps fan-out backend %d failed: %v", i+1, r.err)
			continue
		}
		merged = append(merged, r.models...)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(psResponse{Models: merged}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// buildContinuationRequest creates a new request body with the assistant's partial output
// and a user "Continue." message appended. Returns nil if the API style doesn't support it.
func buildContinuationRequest(originalBody []byte, assistantOutput string, style apiStyle) []byte {
	// Only message-based APIs support continuation
	if style != StyleOllama && style != StyleOpenAIChat && style != StyleAnthropic {
		return nil
	}

	var req map[string]any
	if err := json.Unmarshal(originalBody, &req); err != nil {
		return nil
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return nil // /api/generate has no messages — skip
	}

	// Append assistant message with partial output, then user "Continue."
	messages = append(messages,
		map[string]any{"role": "assistant", "content": assistantOutput},
		map[string]any{"role": "user", "content": "Continue."},
	)
	req["messages"] = messages

	modifiedBody, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	return modifiedBody
}

// makeContinuationRequest builds and executes a continuation request.
// Returns the response, or nil if the request couldn't be built or failed.
func makeContinuationRequest(ctx context.Context, origReq *http.Request, client *http.Client, endpoint string, reqBody []byte, assistantOutput string, style apiStyle, token string) *http.Response {
	contBody := buildContinuationRequest(reqBody, assistantOutput, style)
	if contBody == nil {
		return nil
	}
	contReq, err := http.NewRequestWithContext(ctx, origReq.Method, endpoint, bytes.NewReader(contBody))
	if err != nil {
		log.Printf("[proxy] failed to create continuation request: %v", err)
		return nil
	}
	for key, values := range origReq.Header {
		if key == "Authorization" {
			continue
		}
		for _, value := range values {
			contReq.Header.Add(key, value)
		}
	}
	contReq.Header.Set("Authorization", "Bearer "+token)
	contReq.Host = "private-llm-server"

	resp, err := client.Do(contReq)
	if err != nil {
		log.Printf("[proxy] continuation request failed: %v", err)
		return nil
	}
	return resp
}

// mergeNonStreamingResponse merges two non-streaming JSON response bodies by
// appending the continuation's content to the original response.
func mergeNonStreamingResponse(first, second []byte, style apiStyle) []byte {
	switch style {
	case StyleOllama:
		return mergeOllamaResponse(first, second)
	case StyleOpenAIChat:
		return mergeOpenAIChatResponse(first, second)
	case StyleAnthropic:
		return mergeAnthropicResponse(first, second)
	default:
		return first
	}
}

func mergeOllamaResponse(first, second []byte) []byte {
	var a, b map[string]any
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
		return first
	}

	// Merge message.content (for /api/chat)
	aMsg, aOK := a["message"].(map[string]any)
	bMsg, bOK := b["message"].(map[string]any)
	if aOK && bOK {
		aContent, _ := aMsg["content"].(string)
		bContent, _ := bMsg["content"].(string)
		aMsg["content"] = aContent + bContent

		// Merge tool_calls arrays
		if bTC, ok := bMsg["tool_calls"].([]any); ok && len(bTC) > 0 {
			aTC, _ := aMsg["tool_calls"].([]any)
			aMsg["tool_calls"] = append(aTC, bTC...)
		}
	}

	// Merge response (for /api/generate)
	if aResp, ok := a["response"].(string); ok {
		if bResp, ok := b["response"].(string); ok {
			a["response"] = aResp + bResp
		}
	}

	merged, err := json.Marshal(a)
	if err != nil {
		return first
	}
	return merged
}

func mergeOpenAIChatResponse(first, second []byte) []byte {
	var a, b map[string]any
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
		return first
	}

	aChoices, aOK := a["choices"].([]any)
	bChoices, bOK := b["choices"].([]any)
	if !aOK || !bOK || len(aChoices) == 0 || len(bChoices) == 0 {
		return first
	}

	aChoice, aOK := aChoices[0].(map[string]any)
	bChoice, bOK := bChoices[0].(map[string]any)
	if !aOK || !bOK {
		return first
	}

	aMessage, aOK := aChoice["message"].(map[string]any)
	bMessage, bOK := bChoice["message"].(map[string]any)
	if !aOK || !bOK {
		return first
	}

	// Concatenate content
	aContent, _ := aMessage["content"].(string)
	bContent, _ := bMessage["content"].(string)
	aMessage["content"] = aContent + bContent

	// Merge tool_calls
	if bTC, ok := bMessage["tool_calls"].([]any); ok && len(bTC) > 0 {
		aTC, _ := aMessage["tool_calls"].([]any)
		aMessage["tool_calls"] = append(aTC, bTC...)
	}

	merged, err := json.Marshal(a)
	if err != nil {
		return first
	}
	return merged
}

func mergeAnthropicResponse(first, second []byte) []byte {
	var a, b map[string]any
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
		return first
	}

	aContent, aOK := a["content"].([]any)
	bContent, bOK := b["content"].([]any)
	if !aOK || !bOK {
		return first
	}

	// Append all content blocks from continuation
	a["content"] = append(aContent, bContent...)

	merged, err := json.Marshal(a)
	if err != nil {
		return first
	}
	return merged
}

// terminationAwareWriter buffers lines from the upstream response, forwarding
// content lines to the client immediately while holding back termination signals.
type terminationAwareWriter struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	hasFlusher bool
	style      APIStyle
	feedCh     chan<- []byte
	feedSync   chan struct{} // sync channel for feed consumer
	isCont     bool          // true for continuation responses (strip preamble)

	// Line buffering
	buf       bytes.Buffer
	lastEvent string // SSE event tracking for Anthropic/Responses

	// Held termination lines (not yet written to client)
	heldLines [][]byte

	// Preamble stripping state for continuations
	seenContent bool // have we seen actual content yet?
}

func newTerminationAwareWriter(w http.ResponseWriter, flusher http.Flusher, hasFlusher bool, style APIStyle, feedCh chan<- []byte, feedSync chan struct{}, isCont bool) *terminationAwareWriter {
	return &terminationAwareWriter{
		w:          w,
		flusher:    flusher,
		hasFlusher: hasFlusher,
		style:      style,
		feedCh:     feedCh,
		feedSync:   feedSync,
		isCont:     isCont,
	}
}

// StreamResponse reads the upstream body and processes each line.
func (tw *terminationAwareWriter) StreamResponse(body io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			tw.buf.Write(buf[:n])
			tw.processLines(false)
		}
		if err != nil {
			break
		}
	}
	// Process any remaining partial line
	tw.processLines(true)
}

func (tw *terminationAwareWriter) processLines(flush bool) {
	for {
		content := tw.buf.String()
		idx := strings.Index(content, "\n")
		if idx < 0 {
			if flush && content != "" {
				// Treat remaining content as a final line
				tw.buf.Reset()
				tw.handleLine(strings.TrimRight(content, "\r"))
			}
			break
		}
		line := content[:idx]
		tw.buf.Reset()
		tw.buf.WriteString(content[idx+1:])
		tw.handleLine(strings.TrimRight(line, "\r"))
	}
}

func (tw *terminationAwareWriter) handleLine(line string) {
	if tw.isTerminationLine(line) {
		// Hold termination — still feed to parser for token counting
		tw.heldLines = append(tw.heldLines, []byte(line+"\n"))
		tw.feedCh <- []byte(line + "\n")
		return
	}

	// For continuations, strip preamble framing events
	if tw.isCont && tw.isPreambleLine(line) {
		// Feed to parser (for token counting) but don't write to client
		tw.feedCh <- []byte(line + "\n")
		return
	}

	if !tw.isEmptyOrEventOnly(line) {
		tw.seenContent = true
	}

	// Content line — write to client immediately
	if _, err := tw.w.Write([]byte(line + "\n")); err != nil {
		log.Printf("[proxy] write error: %v", err)
		return
	}
	if tw.hasFlusher {
		tw.flusher.Flush()
	}
	tw.feedCh <- []byte(line + "\n")

	// Track SSE event for Anthropic/Responses
	if strings.HasPrefix(line, "event: ") {
		tw.lastEvent = strings.TrimPrefix(line, "event: ")
	}
}

func (tw *terminationAwareWriter) isTerminationLine(line string) bool {
	switch tw.style {
	case StyleOllama:
		// Ollama: JSON line with "done": true
		if strings.Contains(line, `"done"`) {
			var obj struct{ Done bool }
			if json.Unmarshal([]byte(line), &obj) == nil && obj.Done {
				return true
			}
		}
	case StyleOpenAIChat:
		return strings.TrimSpace(line) == "data: [DONE]"
	case StyleAnthropic:
		// message_stop event and its data line
		if strings.TrimSpace(line) == "event: message_stop" {
			tw.lastEvent = "message_stop"
			return true
		}
		if tw.lastEvent == "message_stop" && strings.HasPrefix(line, "data: ") {
			return true
		}
	case StyleOpenAIResponses:
		if strings.TrimSpace(line) == "event: response.completed" {
			tw.lastEvent = "response.completed"
			return true
		}
		if tw.lastEvent == "response.completed" && strings.HasPrefix(line, "data: ") {
			return true
		}
	}
	return false
}

// isPreambleLine returns true for framing events that should be stripped from
// continuation responses (they'd confuse the client with duplicate starts).
func (tw *terminationAwareWriter) isPreambleLine(line string) bool {
	if !tw.isCont || tw.seenContent {
		return false
	}

	switch tw.style {
	case StyleOpenAIChat:
		// Skip first chunk that has delta.role (role-setting chunk)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var obj struct {
				Choices []struct {
					Delta struct {
						Role string `json:"role"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal([]byte(data), &obj) == nil && len(obj.Choices) > 0 && obj.Choices[0].Delta.Role != "" {
				return true
			}
		}
	case StyleAnthropic:
		trimmed := strings.TrimSpace(line)
		if trimmed == "event: message_start" || trimmed == "event: content_block_start" {
			tw.lastEvent = strings.TrimPrefix(trimmed, "event: ")
			return true
		}
		if (tw.lastEvent == "message_start" || tw.lastEvent == "content_block_start") && strings.HasPrefix(line, "data: ") {
			return true
		}
	case StyleOpenAIResponses:
		trimmed := strings.TrimSpace(line)
		if trimmed == "event: response.created" || trimmed == "event: response.output_item.added" {
			tw.lastEvent = strings.TrimPrefix(trimmed, "event: ")
			return true
		}
		if (tw.lastEvent == "response.created" || tw.lastEvent == "response.output_item.added") && strings.HasPrefix(line, "data: ") {
			return true
		}
	}
	return false
}

func (tw *terminationAwareWriter) isEmptyOrEventOnly(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "event: ")
}

// WaitFed ensures the feed consumer goroutine has processed all lines sent
// by this writer. Call before checking CompletionState.
func (tw *terminationAwareWriter) WaitFed() {
	tw.feedSync <- struct{}{} // request sync
	<-tw.feedSync             // wait for confirmation
}

// ReleaseTermination writes held termination lines to the client.
func (tw *terminationAwareWriter) ReleaseTermination(flusher http.Flusher, hasFlusher bool) {
	for _, line := range tw.heldLines {
		_, _ = tw.w.Write(line)
	}
	if hasFlusher {
		flusher.Flush()
	}
	tw.heldLines = nil
}

// DiscardTermination drops held termination lines (for continuation).
func (tw *terminationAwareWriter) DiscardTermination() {
	tw.heldLines = nil
}
