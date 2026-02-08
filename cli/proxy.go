package main

import (
	"context"
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
	setupMu         sync.Mutex
	vmIP            string
	rotatedOnce     bool
	proxyReady      atomic.Bool
	lastRequestTime time.Time
	lastRequestMu   sync.RWMutex
)

// ensureSetup verifies the VM is running on every call. If the VM was stopped,
// it re-runs the full startup: rotate certs, start VM, wait for Ollama.
func ensureSetup(ctx context.Context) (string, error) {
	setupMu.Lock()
	defer setupMu.Unlock()

	// Always verify VM is still running, even if we have a cached IP
	if vmIP != "" {
		stopped, err := isVMStopped(ctx)
		if err != nil {
			// If we can't check, assume cached IP is still good;
			// the actual request will fail and trigger retry logic if it's not
			log.Printf("[setup] VM status check failed: %v, using cached IP", err)
			return vmIP, nil
		}
		if !stopped {
			return vmIP, nil
		}
		log.Printf("[setup] VM is stopped, restarting full setup...")
		vmIP = ""
		proxyReady.Store(false)
		rotatedOnce = false
		invalidateTLSConfig()
	}

	if err := ensureFirewallOpen(ctx); err != nil {
		return "", fmt.Errorf("failed to configure firewall: %w", err)
	}

	needsStart, err := isVMStopped(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to check VM status: %w", err)
	}

	// Only rotate certs when VM is stopped — it fetches certs from SM on boot
	if needsStart && !rotatedOnce {
		log.Printf("[setup] rotating certificates (VM will boot with fresh certs)...")
		if err := rotateCerts(ctx); err != nil {
			return "", fmt.Errorf("failed to rotate certs: %w", err)
		}
		rotatedOnce = true
		sendStatus(ctx)
	}

	ip, err := ensureVMRunning(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to start VM: %w", err)
	}

	vmIP = ip
	proxyReady.Store(true)
	return ip, nil
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

// resetProxyState clears cached proxy state so ensureSetup re-discovers the VM
// on the next request. Must be called while holding setupMu.
func resetProxyState() {
	vmIP = ""
	proxyReady.Store(false)
	rotatedOnce = false
	invalidateTLSConfig()
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	reqStart := time.Now()
	ctx := r.Context()

	ip, err := ensureSetup(ctx)
	if err != nil {
		log.Printf("[proxy] setup failed: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

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

	// Retry loop for 502s (Caddy up, Ollama not ready)
	var resp *http.Response
	maxRetries := 12
	for attempt := range maxRetries {
		proxyReq, err := http.NewRequestWithContext(ctx, r.Method, endpoint, r.Body)
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

		resp, err = client.Do(proxyReq)
		if err != nil {
			log.Printf("[proxy] request failed (attempt %d/%d): %v", attempt+1, maxRetries, err)
			// On first failure, reset setup so next attempt re-checks firewall/VM
			if attempt == 0 {
				setupMu.Lock()
				resetProxyState()
				setupMu.Unlock()

				// Re-run setup
				newIP, setupErr := ensureSetup(ctx)
				if setupErr != nil {
					http.Error(w, fmt.Sprintf("Failed to re-setup: %v", setupErr), http.StatusServiceUnavailable)
					return
				}
				ip = newIP
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
	if tuiProg != nil {
		tuiProg.SendEvent(tui.RequestEvent{
			Timestamp:       reqStart,
			Method:          r.Method,
			Path:            r.URL.Path,
			Status:          resp.StatusCode,
			Duration:        time.Since(reqStart),
			Encrypted:       true,
			InputTokens:     inputTok,
			OutputTokens:    outputTok,
			OutputTokPerSec: finalRate,
		})
	}
}
