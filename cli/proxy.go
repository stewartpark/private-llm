package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stewartpark/private-llm/cli/tui"
)

var (
	vmIP            string
	rotatedOnce     bool
	proxyReady      atomic.Bool
	lastRequestTime time.Time
	lastRequestMu   sync.RWMutex
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

	endpoint := fmt.Sprintf("https://%s:8080%s", ip, r.URL.Path)
	if r.URL.RawQuery != "" {
		endpoint += "?" + r.URL.RawQuery
	}

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	// Buffer request body so we can peek at "model" and replay on retries.
	var reqBody []byte
	var modelName string
	if r.Body != nil {
		reqBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
			return
		}
		var peek struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(reqBody, &peek) == nil && peek.Model != "" {
			modelName = peek.Model
		}
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
				endpoint = fmt.Sprintf("https://%s:8080%s", ip, r.URL.Path)
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
	feedCh := make(chan []byte, 64)
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		for chunk := range feedCh {
			tp.Feed(chunk)
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

	// Stream with flushing — writes go to client immediately,
	// copies are sent to the token parser goroutine asynchronously.
	flusher, ok := w.(http.Flusher)
	if !ok {
		body, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(body)
		feedCh <- body
	} else {
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					log.Printf("[proxy] write error: %v", writeErr)
					break
				}
				flusher.Flush()
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				feedCh <- chunk
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Printf("[proxy] read error: %v", err)
				break
			}
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
