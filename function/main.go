package function

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/firestore"
)

var (
	projectID    = os.Getenv("GCP_PROJECT")
	zone         = os.Getenv("GCP_ZONE")
	vmName       = os.Getenv("VM_NAME")
	databaseID   = os.Getenv("FIRESTORE_DATABASE")
	idleTimeout  = 300
	tlsConfig    *tls.Config
	apiToken     string

	// Cached VM IP
	cachedIP   string
	cachedIPMu sync.RWMutex
)

func init() {
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if zone == "" {
		zone = "us-central1-a"
	}
	if vmName == "" {
		vmName = "private-llm-vm"
	}
	if databaseID == "" {
		databaseID = "private-llm"
	}
	if t := os.Getenv("IDLE_TIMEOUT"); t != "" {
		if v, err := strconv.Atoi(t); err == nil {
			idleTimeout = v
		}
	}

	// Load external API token
	apiToken = os.Getenv("API_TOKEN")
	if apiToken == "" {
		log.Printf("[init] WARNING: API_TOKEN not set")
	} else {
		log.Printf("[init] API token loaded")
	}

	// Load mTLS certs from Secret Manager (via secret env vars)
	caCert := []byte(os.Getenv("CA_CERT"))
	clientCert := []byte(os.Getenv("CLIENT_CERT"))
	clientKey := []byte(os.Getenv("CLIENT_KEY"))

	if len(caCert) > 0 && len(clientCert) > 0 && len(clientKey) > 0 {
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			log.Printf("[init] ERROR: failed to parse CA cert")
		}

		cert, err := tls.X509KeyPair(clientCert, clientKey)
		if err != nil {
			log.Printf("[init] ERROR: failed to parse client cert/key: %v", err)
		} else {
			tlsConfig = &tls.Config{
				MinVersion:   tls.VersionTLS13,
				RootCAs:      certPool,
				Certificates: []tls.Certificate{cert},
				ServerName:   "private-llm-server",
			}
		}
	} else {
		log.Printf("[init] ERROR: missing certs - CA:%d CLIENT_CERT:%d CLIENT_KEY:%d",
			len(caCert), len(clientCert), len(clientKey))
	}

	if tlsConfig != nil {
		log.Printf("[init] mTLS configured")

		// Log cert expiration
		for _, cert := range tlsConfig.Certificates {
			if len(cert.Certificate) > 0 {
				x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
				if err == nil {
					daysUntilExpiry := time.Until(x509Cert.NotAfter).Hours() / 24
					log.Printf("[init] Client cert expires in %.0f days (%s)",
						daysUntilExpiry, x509Cert.NotAfter.Format("2006-01-02"))
				}
			}
		}
	} else {
		log.Printf("[init] WARNING: mTLS not loaded")
	}
}

// getVMIP returns the VM's internal IP.
// If instance is provided, extracts and caches the IP. If instance is nil, returns the cached IP.
func getVMIP(instance *computepb.Instance) string {
	if instance != nil {
		if len(instance.GetNetworkInterfaces()) > 0 {
			ip := instance.GetNetworkInterfaces()[0].GetNetworkIP()
			log.Printf("[getVMIP] internal IP: %s", ip)
			cachedIPMu.Lock()
			cachedIP = ip
			cachedIPMu.Unlock()
			return ip
		}
	}
	cachedIPMu.RLock()
	defer cachedIPMu.RUnlock()
	return cachedIP
}

// VMState represents the Firestore document structure for VM state tracking
type VMState struct {
	LastRequestUnix int64 `firestore:"last_request_unix"`
	Provisioned     bool  `firestore:"provisioned"`
}

const provisioningIdleTimeout = 1800 // 30 minutes for provisioning VMs

// PrivateLlmProxy is the HTTP entry point for the proxy function
func PrivateLlmProxy(w http.ResponseWriter, r *http.Request) {
	log.Printf("[proxy] request: %s %s", r.Method, r.URL.Path)

	// Check if API token is configured
	if apiToken == "" {
		log.Printf("[proxy] API token not configured")
		http.Error(w, "Service unavailable: API token not configured", http.StatusServiceUnavailable)
		return
	}

	// Validate mTLS configuration
	if tlsConfig == nil {
		log.Printf("[proxy] FATAL: mTLS certs not loaded")
		http.Error(w, "Internal server error: mTLS not configured", http.StatusInternalServerError)
		return
	}

	// Validate Bearer token
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	providedToken := strings.TrimPrefix(authHeader, "Bearer ")

	// Constant-time comparison
	if !secureCompare(providedToken, apiToken) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Printf("[proxy] token validated")

	ctx := r.Context()

	// Update last request timestamp FIRST (even if provisioning/503)
	// This prevents idle monitoring from stopping VM during installation
	updateLastRequest(ctx)

	// Start heartbeat to keep updating during long requests
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[proxy] client disconnected, stopping heartbeat")
				return
			case <-ticker.C:
				updateLastRequest(ctx)
			}
		}
	}()

	// Ensure VM is running
	log.Printf("[proxy] ensuring VM is running...")
	ip, err := ensureVMRunning(ctx)
	if err != nil {
		log.Printf("[proxy] ensureVMRunning failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to start VM: %v", err), http.StatusServiceUnavailable)
		return
	}
	log.Printf("[proxy] VM ready at IP: %s", ip)

	endpoint := fmt.Sprintf("https://%s:8080%s", ip, r.URL.Path)
	if r.URL.RawQuery != "" {
		endpoint += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(ctx, r.Method, endpoint, r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
		return
	}

	// Copy all headers EXCEPT Authorization (we'll set internal token separately)
	for key, values := range r.Header {
		if key == "Authorization" {
			continue // Skip external auth header
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Set internal token for VM authentication
	proxyReq.Header.Set("Authorization", "Bearer "+os.Getenv("INTERNAL_TOKEN"))
	proxyReq.Host = "private-llm-server"
	log.Printf("[proxy] set internal token for VM authentication")

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
	log.Printf("[proxy] forwarding request to: %s", endpoint)

	// Retry on 502 (Caddy up, Ollama not ready yet)
	var resp *http.Response
	maxRetries := 12 // 1 minute total (5s * 12)
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Need to recreate the request for retry (body may have been consumed)
			proxyReq, err = http.NewRequestWithContext(ctx, r.Method, endpoint, r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
				return
			}
			// Reapply headers
			for key, values := range r.Header {
				if key == "Authorization" {
					continue
				}
				for _, value := range values {
					proxyReq.Header.Add(key, value)
				}
			}
			proxyReq.Header.Set("Authorization", "Bearer "+os.Getenv("INTERNAL_TOKEN"))
			proxyReq.Host = "private-llm-server"
		}

		resp, err = client.Do(proxyReq)
		if err != nil {
			log.Printf("[proxy] request failed (attempt %d/%d): %v", attempt+1, maxRetries, err)
			if attempt < maxRetries-1 {
				time.Sleep(5 * time.Second)
				continue
			}
			http.Error(w, fmt.Sprintf("Failed to reach VM: %v", err), http.StatusBadGateway)
			return
		}

		// If we get 502, Caddy is up but Ollama isn't ready - retry
		if resp.StatusCode == http.StatusBadGateway {
			_ = resp.Body.Close()
			log.Printf("[proxy] received 502 (attempt %d/%d), Ollama not ready, retrying...", attempt+1, maxRetries)
			if attempt < maxRetries-1 {
				time.Sleep(5 * time.Second)
				continue
			}
			// Final attempt failed
			http.Error(w, "Ollama service not ready after retries", http.StatusBadGateway)
			return
		}

		// Success or other error - break and proceed
		break
	}
	defer resp.Body.Close()
	log.Printf("[proxy] response status: %d", resp.StatusCode)

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Enable streaming by flushing after each write
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[proxy] ResponseWriter doesn't support flushing")
		_, _ = io.Copy(w, resp.Body)
		return
	}

	// Stream with explicit flushing
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				log.Printf("[proxy] write error: %v", writeErr)
				return
			}
			flusher.Flush() // Force send to client immediately
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

func updateLastRequest(ctx context.Context) {
	log.Printf("[updateLastRequest] attempting update (project=%s, db=%s, doc=%s)", projectID, databaseID, vmName)

	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		log.Printf("[updateLastRequest] ERROR: failed to create Firestore client: %v", err)
		return
	}
	defer client.Close()

	now := time.Now().Unix()
	docPath := fmt.Sprintf("vm_state/%s", vmName)
	docRef := client.Collection("vm_state").Doc(vmName)

	log.Printf("[updateLastRequest] writing to path: %s, timestamp: %d", docPath, now)

	// Upsert - create document/field if doesn't exist, update if exists
	_, err = docRef.Set(ctx, map[string]interface{}{
		"last_request_unix": now,
	}, firestore.MergeAll)

	if err != nil {
		log.Printf("[updateLastRequest] ERROR: Failed to update Firestore: %v", err)
	} else {
		log.Printf("[updateLastRequest] SUCCESS: Updated timestamp to %d", now)
	}
}

type PubSubMessage struct {
	Data []byte `json:"data"`
}

func IdleMonitoring(ctx context.Context, m PubSubMessage) error {
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return fmt.Errorf("failed to create Firestore client: %w", err)
	}
	defer client.Close()

	docRef := client.Collection("vm_state").Doc(vmName)
	docSnap, err := docRef.Get(ctx)
	if err != nil {
		log.Printf("[IdleMonitoring] no state document found: %v", err)
		return nil // No document means no activity, but don't fail
	}

	var state VMState
	if err := docSnap.DataTo(&state); err != nil {
		return fmt.Errorf("failed to parse VM state: %w", err)
	}

	// Use longer timeout if VM is still provisioning
	timeout := idleTimeout
	if !state.Provisioned {
		timeout = provisioningIdleTimeout
		log.Printf("[IdleMonitoring] VM provisioning, using extended timeout (%ds)", timeout)
	}

	// Calculate elapsed time since last request
	elapsed := time.Now().Unix() - state.LastRequestUnix
	if elapsed < int64(timeout) {
		log.Printf("[IdleMonitoring] VM active (last request %ds ago, timeout %ds)", elapsed, timeout)
		return nil
	}

	log.Printf("[IdleMonitoring] VM idle (last request %ds ago, timeout %ds), stopping...", elapsed, timeout)

	computeClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return err
	}
	defer computeClient.Close()

	instance, err := computeClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	})
	if err != nil {
		return err
	}

	if instance.GetStatus() != "RUNNING" {
		return nil
	}

	_, err = computeClient.Stop(ctx, &computepb.StopInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	})
	return err
}

func ensureVMRunning(ctx context.Context) (string, error) {
	log.Printf("[ensureVM] creating compute client (project=%s, zone=%s, vm=%s)", projectID, zone, vmName)
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	log.Printf("[ensureVM] getting instance status...")
	instance, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get instance: %w", err)
	}

	status := instance.GetStatus()
	log.Printf("[ensureVM] instance status: %s", status)

	// Wait for transitional states to complete
	for status == "STOPPING" || status == "STAGING" || status == "SUSPENDING" {
		log.Printf("[ensureVM] VM in transitional state %s, waiting...", status)
		time.Sleep(5 * time.Second)
		instance, err = client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  projectID,
			Zone:     zone,
			Instance: vmName,
		})
		if err != nil {
			return "", fmt.Errorf("failed to get instance: %w", err)
		}
		status = instance.GetStatus()
		log.Printf("[ensureVM] instance status: %s", status)
	}

	if status == "RUNNING" {
		ip := getVMIP(instance)
		log.Printf("[ensureVM] VM running, IP: %s, waiting for Ollama...", ip)
		if err := waitForOllama(ctx, ip); err != nil {
			return "", err
		}
		return ip, nil
	}

	if status == "TERMINATED" || status == "STOPPED" || status == "SUSPENDED" {
		_, err := client.Start(ctx, &computepb.StartInstanceRequest{
			Project:  projectID,
			Zone:     zone,
			Instance: vmName,
		})
		if err != nil {
			return "", fmt.Errorf("failed to start instance: %w", err)
		}

		// Wait for VM to be running and get new IP
		var ip string
		for i := 0; i < 60; i++ {
			time.Sleep(5 * time.Second)
			instance, err = client.Get(ctx, &computepb.GetInstanceRequest{
				Project:  projectID,
				Zone:     zone,
				Instance: vmName,
			})
			if err != nil {
				continue
			}
			if instance.GetStatus() == "RUNNING" {
				ip = getVMIP(instance)
				break
			}
		}

		if err := waitForOllama(ctx, ip); err != nil {
			return "", err
		}
		return ip, nil
	}

	return "", fmt.Errorf("VM in unexpected state: %s", status)
}

func waitForOllama(ctx context.Context, ip string) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
	endpoint := fmt.Sprintf("https://%s:8080/api/tags", ip)
	log.Printf("[waitForOllama] polling endpoint: %s", endpoint)

	for i := 0; i < 60; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Host = "private-llm-server"
		req.Header.Set("Authorization", "Bearer "+os.Getenv("INTERNAL_TOKEN"))
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			log.Printf("[waitForOllama] server responding after %d attempts (status=%d)", i+1, resp.StatusCode)
			return nil // Any HTTP response means server is up
		}
		log.Printf("[waitForOllama] attempt %d: err=%v", i+1, err)
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for Ollama to be ready")
}

// secureCompare performs constant-time string comparison
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
