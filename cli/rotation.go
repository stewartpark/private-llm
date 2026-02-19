package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
)

var (
	pinnedFingerprint [32]byte
	pinnedFPMu        sync.RWMutex
)

// rotateCerts generates new server cert + client cert + token on every VM start.
// CA is only regenerated if within 30 days of expiry.
// Server cert fingerprint is pinned in memory for impersonation detection.
func rotateCerts(ctx context.Context) error {
	certDir, err := CertsDir()
	if err != nil {
		return fmt.Errorf("failed to get cert directory: %w", err)
	}
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs dir: %w", err)
	}

	// Load or generate CA
	caCertPEM, caKeyPEM, err := ensureCA(certDir)
	if err != nil {
		return fmt.Errorf("failed to ensure CA: %w", err)
	}

	// Generate new server cert + key (signed by CA)
	log.Printf("[rotation] generating new server certificate...")
	serverCertPEM, serverKeyPEM, _, err := generateServerCert(caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate server cert: %w", err)
	}

	// Pin the server cert fingerprint in memory
	block, _ := pem.Decode(serverCertPEM)
	if block == nil {
		return fmt.Errorf("failed to decode server cert PEM for pinning")
	}
	fp := sha256.Sum256(block.Bytes)
	pinnedFPMu.Lock()
	pinnedFingerprint = fp
	pinnedFPMu.Unlock()
	log.Printf("[rotation] pinned server cert fingerprint: %x", fp[:8])

	// Generate new client cert + key (signed by CA)
	log.Printf("[rotation] generating new client certificate...")
	clientCertPEM, clientKeyPEM, _, err := generateClientCert(caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate client cert: %w", err)
	}

	// Generate new token
	log.Printf("[rotation] generating new internal token...")
	token, err := generateToken(64)
	if err != nil {
		return fmt.Errorf("failed to generate token: %w", err)
	}

	// Write client certs + token to local disk
	localFiles := map[string][]byte{
		"client.crt": clientCertPEM,
		"client.key": clientKeyPEM,
		"token":      []byte(token),
	}
	for name, data := range localFiles {
		p := filepath.Join(certDir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}

	// Write server cert + key + CA cert + token to Secret Manager (VM needs these)
	smClient, err := secretmanager.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer smClient.Close() //nolint:errcheck

	secrets := map[string][]byte{
		"private-llm-server-cert":    serverCertPEM,
		"private-llm-server-key":     serverKeyPEM,
		"private-llm-ca-cert":        caCertPEM,
		"private-llm-internal-token": []byte(token),
	}
	for secretID, data := range secrets {
		if err := writeSecretVersion(ctx, smClient, secretID, data); err != nil {
			return fmt.Errorf("failed to write %s: %w", secretID, err)
		}
	}

	// Invalidate TLS cache so next request uses fresh certs
	invalidateTLSConfig()

	log.Printf("[rotation] cert rotation complete")
	return nil
}

// ensureCA loads existing CA or generates a new one. Regenerates if within 30 days of expiry.
func ensureCA(certDir string) (caCertPEM, caKeyPEM []byte, err error) {
	caCertPath := filepath.Join(certDir, "ca.crt")
	caKeyPath := filepath.Join(certDir, "ca.key")

	caCertPEM, certErr := os.ReadFile(caCertPath) //nolint:gosec // path from known config dir
	caKeyPEM, keyErr := os.ReadFile(caKeyPath)    //nolint:gosec // path from known config dir

	if certErr == nil && keyErr == nil {
		// Check expiry
		block, _ := pem.Decode(caCertPEM)
		if block != nil {
			cert, parseErr := x509.ParseCertificate(block.Bytes)
			if parseErr == nil && time.Until(cert.NotAfter) > 30*24*time.Hour {
				log.Printf("[rotation] existing CA valid until %s", cert.NotAfter.Format("2006-01-02"))
				return caCertPEM, caKeyPEM, nil
			}
			log.Printf("[rotation] CA expires within 30 days, regenerating...")
		}
	}

	log.Printf("[rotation] generating new CA certificate (10-year validity)...")
	caCertPEM, caKeyPEM, err = generateCA()
	if err != nil {
		return nil, nil, err
	}

	if err := os.WriteFile(caCertPath, caCertPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write ca.crt: %w", err)
	}
	if err := os.WriteFile(caKeyPath, caKeyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write ca.key: %w", err)
	}

	return caCertPEM, caKeyPEM, nil
}

// generateCA creates a new self-signed CA certificate (10-year validity).
func generateCA() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Private LLM CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return certPEM, keyPEM, nil
}

// generateServerCert creates a new server certificate signed by the CA.
func generateServerCert(caCertPEM, caKeyPEM []byte) ([]byte, []byte, time.Time, error) {
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA key: %w", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to generate server key: %w", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	notAfter := time.Now().Add(7 * 24 * time.Hour)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "private-llm-server",
		},
		NotBefore:             time.Now(),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"private-llm-server"},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to create server cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return certPEM, keyPEM, notAfter, nil
}

// generateClientCert creates a new client certificate signed by the CA.
func generateClientCert(caCertPEM, caKeyPEM []byte) ([]byte, []byte, time.Time, error) {
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA key: %w", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to generate client key: %w", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	notAfter := time.Now().Add(7 * 24 * time.Hour)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "private-llm-client",
		},
		NotBefore:             time.Now(),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to create client cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return certPEM, keyPEM, notAfter, nil
}

// generateToken creates a cryptographically random hex token.
func generateToken(length int) (string, error) {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// secretHasVersions checks whether a Secret Manager secret already has at least one version.
func secretHasVersions(ctx context.Context, client *secretmanager.Client, secretID string) (bool, error) {
	it := client.ListSecretVersions(ctx, &secretmanagerpb.ListSecretVersionsRequest{
		Parent:   fmt.Sprintf("projects/%s/secrets/%s", cfg.ProjectID, secretID),
		PageSize: 1,
	})
	_, err := it.Next()
	if err != nil {
		if err == iterator.Done {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// writeSecretVersion adds a new version to an existing Secret Manager secret.
func writeSecretVersion(ctx context.Context, client *secretmanager.Client, secretID string, data []byte) error {
	_, err := client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: fmt.Sprintf("projects/%s/secrets/%s", cfg.ProjectID, secretID),
		Payload: &secretmanagerpb.SecretPayload{
			Data: data,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add secret version for %s: %w", secretID, err)
	}
	log.Printf("[rotation] wrote new version for %s", secretID)
	return nil
}

// getPinnedFingerprint returns the current pinned server cert fingerprint.
func getPinnedFingerprint() [32]byte {
	pinnedFPMu.RLock()
	defer pinnedFPMu.RUnlock()
	return pinnedFingerprint
}
