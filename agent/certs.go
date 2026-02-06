package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
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

// getTLSConfig returns a cached mTLS config, refreshing from Secret Manager if expired.
func getTLSConfig(ctx context.Context) (*tls.Config, string, error) {
	certCacheMu.RLock()
	if certCache != nil && time.Since(certCache.loadedAt) < certCacheTTL {
		cfg := certCache.tlsConfig
		token := certCache.internalToken
		certCacheMu.RUnlock()
		return cfg, token, nil
	}
	certCacheMu.RUnlock()

	return refreshTLSConfig(ctx)
}

// invalidateTLSConfig forces a cert refresh on next getTLSConfig call.
func invalidateTLSConfig() {
	certCacheMu.Lock()
	certCache = nil
	certCacheMu.Unlock()
}

// refreshTLSConfig loads certs from Secret Manager and builds a tls.Config.
func refreshTLSConfig(ctx context.Context) (*tls.Config, string, error) {
	certCacheMu.Lock()
	defer certCacheMu.Unlock()

	log.Printf("[certs] loading from Secret Manager...")

	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	caCert, err := accessSecret(ctx, client, "private-llm-ca-cert")
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch CA cert: %w", err)
	}

	clientCert, err := accessSecret(ctx, client, "private-llm-client-cert")
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch client cert: %w", err)
	}

	clientKey, err := accessSecret(ctx, client, "private-llm-client-key")
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch client key: %w", err)
	}

	internalToken, err := accessSecret(ctx, client, "private-llm-internal-token")
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch internal token: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, "", fmt.Errorf("failed to parse CA cert")
	}

	cert, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse client cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      certPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   "private-llm-server",
	}

	token := string(internalToken)

	certCache = &certCacheEntry{
		tlsConfig:     tlsCfg,
		internalToken: token,
		loadedAt:      time.Now(),
	}

	log.Printf("[certs] loaded successfully")
	return tlsCfg, token, nil
}

// accessSecret fetches the latest version of a secret from Secret Manager.
func accessSecret(ctx context.Context, client *secretmanager.Client, secretID string) ([]byte, error) {
	result, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", cfg.ProjectID, secretID),
	})
	if err != nil {
		return nil, err
	}
	return result.Payload.Data, nil
}
