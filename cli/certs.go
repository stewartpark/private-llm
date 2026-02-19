package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	certCache   *certCacheEntry
	certCacheMu sync.RWMutex
)

type certCacheEntry struct {
	tlsConfig     *tls.Config
	internalToken string
	loadedAt      time.Time
}

const certCacheTTL = 30 * time.Minute

// getTLSConfig returns a cached mTLS config, refreshing from local disk if expired.
func getTLSConfig(_ context.Context) (*tls.Config, string, error) {
	certCacheMu.RLock()
	if certCache != nil && time.Since(certCache.loadedAt) < certCacheTTL {
		cfg := certCache.tlsConfig
		token := certCache.internalToken
		certCacheMu.RUnlock()
		return cfg, token, nil
	}
	certCacheMu.RUnlock()

	return refreshTLSConfig()
}

// invalidateTLSConfig forces a cert refresh on next getTLSConfig call.
func invalidateTLSConfig() {
	certCacheMu.Lock()
	certCache = nil
	certCacheMu.Unlock()
}

// refreshTLSConfig loads certs from local disk and builds a tls.Config.
func refreshTLSConfig() (*tls.Config, string, error) {
	certCacheMu.Lock()
	defer certCacheMu.Unlock()

	certDir, err := CertsDir()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get cert directory: %w", err)
	}
	log.Printf("[certs] loading from %s...", certDir)

	caCertPEM, err := os.ReadFile(filepath.Join(certDir, "ca.crt")) //nolint:gosec // path is from known config dir
	if err != nil {
		return nil, "", fmt.Errorf("failed to read CA cert: %w", err)
	}

	clientCertPEM, err := os.ReadFile(filepath.Join(certDir, "client.crt")) //nolint:gosec // path is from known config dir
	if err != nil {
		return nil, "", fmt.Errorf("failed to read client cert: %w", err)
	}

	clientKeyPEM, err := os.ReadFile(filepath.Join(certDir, "client.key")) //nolint:gosec // path is from known config dir
	if err != nil {
		return nil, "", fmt.Errorf("failed to read client key: %w", err)
	}

	tokenBytes, err := os.ReadFile(filepath.Join(certDir, "token")) //nolint:gosec // path is from known config dir
	if err != nil {
		return nil, "", fmt.Errorf("failed to read token: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCertPEM) {
		return nil, "", fmt.Errorf("failed to parse CA cert")
	}

	cert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse client cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      certPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   "private-llm-server",
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no server certificate presented")
			}
			fp := sha256.Sum256(rawCerts[0])
			pinned := getPinnedFingerprint()
			// Only check if we have a pinned fingerprint (all zeros = not yet pinned)
			if pinned != [32]byte{} && fp != pinned {
				return fmt.Errorf("server cert fingerprint mismatch (possible impersonation)")
			}
			return nil
		},
	}

	token := string(tokenBytes)

	certCache = &certCacheEntry{
		tlsConfig:     tlsCfg,
		internalToken: token,
		loadedAt:      time.Now(),
	}

	log.Printf("[certs] loaded successfully")
	return tlsCfg, token, nil
}
