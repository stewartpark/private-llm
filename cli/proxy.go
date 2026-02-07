package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	setupMu   sync.Mutex
	vmIP      string
	rotatedOnce bool
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
	}

	ip, err := ensureVMRunning(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to start VM: %w", err)
	}

	vmIP = ip
	return ip, nil
}

// resetSetup forces re-running firewall + VM checks on next request.
func resetSetup() {
	setupMu.Lock()
	vmIP = ""
	setupMu.Unlock()
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("[proxy] %s %s", r.Method, r.URL.Path)
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
				resetSetup()
				invalidateTLSConfig()

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
	defer resp.Body.Close()
	log.Printf("[proxy] response status: %d", resp.StatusCode)

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream with flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, resp.Body)
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				log.Printf("[proxy] write error: %v", writeErr)
				return
			}
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[proxy] read error: %v", err)
			return
		}
	}
}
