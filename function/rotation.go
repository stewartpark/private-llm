package function

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	run "google.golang.org/api/run/v2"
)

// Credentials holds all generated cryptographic materials
type Credentials struct {
	CACert         []byte
	CAKey          []byte
	ServerCert     []byte
	ServerKey      []byte
	ClientCert     []byte
	ClientKey      []byte
	InternalToken  string
	ServerNotAfter time.Time
	ClientNotAfter time.Time
}

// RotationConfig holds the parameters for rotation operations
type RotationConfig struct {
	RotateCA bool `json:"rotate_ca"` // Rotate CA + server + client certs (full regeneration)
	DryRun   bool `json:"dry_run"`   // Preview changes without applying
	Force    bool `json:"force"`     // Skip eligibility checks
}

// SecretRotation is the Pub/Sub entry point for the secret rotation orchestrator
func SecretRotation(ctx context.Context, m PubSubMessage) error {
	// Parse message data (m.Data is already base64-decoded by Cloud Functions)
	var config RotationConfig
	if len(m.Data) > 0 {
		if err := json.Unmarshal(m.Data, &config); err != nil {
			log.Printf("[rotation] Failed to parse message JSON: %v", err)
			return fmt.Errorf("failed to parse message: %w", err)
		}
	}

	log.Printf("[rotation] Request: rotate_ca=%v, dry_run=%v, force=%v", config.RotateCA, config.DryRun, config.Force)

	if config.RotateCA {
		log.Printf("[rotation] CA rotation mode - regenerating CA + server + client certificates")
		if err := handleCARotationPubSub(ctx, config.DryRun); err != nil {
			log.Printf("[rotation] CA rotation failed: %v", err)
			return fmt.Errorf("ca rotation failed: %w", err)
		}
		return nil
	}

	// Normal rotation mode: check eligibility first
	if !config.Force {
		eligible, reason, err := checkRotationEligibility(ctx)
		if err != nil {
			log.Printf("[rotation] Error checking eligibility: %v", err)
			return fmt.Errorf("eligibility check failed: %w", err)
		}
		if !eligible {
			log.Printf("[rotation] Not eligible for rotation: %s", reason)
			return nil // Not an error, just skipped
		}
	}

	if err := handleRotationPubSub(ctx, config.DryRun); err != nil {
		log.Printf("[rotation] Rotation failed: %v", err)
		return fmt.Errorf("rotation failed: %w", err)
	}

	return nil
}

// RotationHandler is the HTTP entry point for the rotation orchestrator (DEPRECATED - kept for backwards compatibility)
func RotationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rotateCA := r.URL.Query().Get("rotate_ca") == "true"
	isDryRun := r.URL.Query().Get("dry_run") == "true"
	forceRotate := r.URL.Query().Get("force") == "true"

	log.Printf("[rotation] Request: rotate_ca=%v, dry_run=%v, force=%v", rotateCA, isDryRun, forceRotate)

	if rotateCA {
		log.Printf("[rotation] CA rotation mode - regenerating CA + server + client certificates")
		if err := handleCARotation(ctx, w, isDryRun); err != nil {
			log.Printf("[rotation] CA rotation failed: %v", err)
			http.Error(w, fmt.Sprintf("CA rotation failed: %v", err), http.StatusInternalServerError)
			return
		}
		return
	}

	// Normal rotation mode: check eligibility first
	if !forceRotate {
		eligible, reason, err := checkRotationEligibility(ctx)
		if err != nil {
			log.Printf("[rotation] Error checking eligibility: %v", err)
			http.Error(w, fmt.Sprintf("Eligibility check failed: %v", err), http.StatusInternalServerError)
			return
		}
		if !eligible {
			log.Printf("[rotation] Not eligible for rotation: %s", reason)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "skipped",
				"reason": reason,
			})
			return
		}
	}

	if err := handleRotation(ctx, w, isDryRun); err != nil {
		log.Printf("[rotation] Rotation failed: %v", err)
		http.Error(w, fmt.Sprintf("Rotation failed: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleCARotationPubSub generates new CA and all credentials (Pub/Sub version)
func handleCARotationPubSub(ctx context.Context, dryRun bool) error {
	log.Printf("[rotation] Generating CA certificate (10-year validity)...")
	caCert, caKey, err := generateCA()
	if err != nil {
		return fmt.Errorf("failed to generate CA: %w", err)
	}

	log.Printf("[rotation] Generating server certificate (1-week validity)...")
	serverCert, serverKey, serverNotAfter, err := generateServerCert(caCert, caKey)
	if err != nil {
		return fmt.Errorf("failed to generate server cert: %w", err)
	}

	log.Printf("[rotation] Generating client certificate (1-week validity)...")
	clientCert, clientKey, clientNotAfter, err := generateClientCert(caCert, caKey)
	if err != nil {
		return fmt.Errorf("failed to generate client cert: %w", err)
	}

	log.Printf("[rotation] Generating internal token (64 chars)...")
	internalToken, err := generateToken(64)
	if err != nil {
		return fmt.Errorf("failed to generate internal token: %w", err)
	}

	creds := &Credentials{
		CACert:         caCert,
		CAKey:          caKey,
		ServerCert:     serverCert,
		ServerKey:      serverKey,
		ClientCert:     clientCert,
		ClientKey:      clientKey,
		InternalToken:  internalToken,
		ServerNotAfter: serverNotAfter,
		ClientNotAfter: clientNotAfter,
	}

	log.Printf("[rotation] Validating generated credentials...")
	if err := validateCredentials(creds); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	if dryRun {
		log.Printf("[rotation] Dry run mode - would rotate CA and regenerate all certificates")
		log.Printf("[rotation] CA expires: %s", creds.ServerNotAfter.Add(9*365*24*time.Hour).Format("2006-01-02"))
		log.Printf("[rotation] Server expires: %s", creds.ServerNotAfter.Format("2006-01-02"))
		log.Printf("[rotation] Client expires: %s", creds.ClientNotAfter.Format("2006-01-02"))
		return nil
	}

	log.Printf("[rotation] Creating new secret versions for CA + server + client...")
	projectID := os.Getenv("GCP_PROJECT")
	if err := createSecretVersion(ctx, projectID, "private-llm-ca-cert", creds.CACert); err != nil {
		return fmt.Errorf("failed to create ca-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-ca-key", creds.CAKey); err != nil {
		return fmt.Errorf("failed to create ca-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-cert", creds.ServerCert); err != nil {
		return fmt.Errorf("failed to create server-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-key", creds.ServerKey); err != nil {
		return fmt.Errorf("failed to create server-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-cert", creds.ClientCert); err != nil {
		return fmt.Errorf("failed to create client-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-key", creds.ClientKey); err != nil {
		return fmt.Errorf("failed to create client-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-internal-token", []byte(creds.InternalToken)); err != nil {
		return fmt.Errorf("failed to create internal-token version: %w", err)
	}

	log.Printf("[rotation] CA rotation complete - all certificates regenerated")
	log.Printf("[rotation] CA expires: %s", creds.ServerNotAfter.Add(9*365*24*time.Hour).Format("2006-01-02"))
	log.Printf("[rotation] Server expires: %s", creds.ServerNotAfter.Format("2006-01-02"))
	log.Printf("[rotation] Client expires: %s", creds.ClientNotAfter.Format("2006-01-02"))
	return nil
}

// handleCARotation generates new CA and all credentials - HTTP version
func handleCARotation(ctx context.Context, w http.ResponseWriter, dryRun bool) error {
	log.Printf("[rotation] Generating CA certificate (10-year validity)...")
	caCert, caKey, err := generateCA()
	if err != nil {
		return fmt.Errorf("failed to generate CA: %w", err)
	}

	log.Printf("[rotation] Generating server certificate (1-week validity)...")
	serverCert, serverKey, serverNotAfter, err := generateServerCert(caCert, caKey)
	if err != nil {
		return fmt.Errorf("failed to generate server cert: %w", err)
	}

	log.Printf("[rotation] Generating client certificate (1-week validity)...")
	clientCert, clientKey, clientNotAfter, err := generateClientCert(caCert, caKey)
	if err != nil {
		return fmt.Errorf("failed to generate client cert: %w", err)
	}

	log.Printf("[rotation] Generating internal token (64 chars)...")
	internalToken, err := generateToken(64)
	if err != nil {
		return fmt.Errorf("failed to generate internal token: %w", err)
	}

	creds := &Credentials{
		CACert:         caCert,
		CAKey:          caKey,
		ServerCert:     serverCert,
		ServerKey:      serverKey,
		ClientCert:     clientCert,
		ClientKey:      clientKey,
		InternalToken:  internalToken,
		ServerNotAfter: serverNotAfter,
		ClientNotAfter: clientNotAfter,
	}

	log.Printf("[rotation] Validating generated credentials...")
	if err := validateCredentials(creds); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	if dryRun {
		log.Printf("[rotation] Dry run mode - would rotate CA and regenerate all certificates")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "dry_run",
			"message": "Would rotate CA and regenerate all certificates",
			"credentials": map[string]string{
				"ca_expires":     creds.ServerNotAfter.Add(9 * 365 * 24 * time.Hour).Format("2006-01-02"),
				"server_expires": creds.ServerNotAfter.Format("2006-01-02"),
				"client_expires": creds.ClientNotAfter.Format("2006-01-02"),
			},
		})
		return nil
	}

	log.Printf("[rotation] Creating new secret versions for CA + server + client...")
	projectID := os.Getenv("GCP_PROJECT")
	if err := createSecretVersion(ctx, projectID, "private-llm-ca-cert", creds.CACert); err != nil {
		return fmt.Errorf("failed to create ca-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-cert", creds.ServerCert); err != nil {
		return fmt.Errorf("failed to create server-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-key", creds.ServerKey); err != nil {
		return fmt.Errorf("failed to create server-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-cert", creds.ClientCert); err != nil {
		return fmt.Errorf("failed to create client-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-key", creds.ClientKey); err != nil {
		return fmt.Errorf("failed to create client-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-internal-token", []byte(creds.InternalToken)); err != nil {
		return fmt.Errorf("failed to create internal-token version: %w", err)
	}

	log.Printf("[rotation] CA rotation complete - all certificates regenerated")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "CA rotated - all certificates regenerated",
		"note":    "Restart VM to load new server certificates",
		"credentials": map[string]string{
			"ca_expires":     creds.ServerNotAfter.Add(9 * 365 * 24 * time.Hour).Format("2006-01-02"),
			"server_expires": creds.ServerNotAfter.Format("2006-01-02"),
			"client_expires": creds.ClientNotAfter.Format("2006-01-02"),
		},
	})
	return nil
}

// handleRotationPubSub rotates server/client certs and internal token (Pub/Sub version)
func handleRotationPubSub(ctx context.Context, dryRun bool) error {
	projectID := os.Getenv("GCP_PROJECT")

	log.Printf("[rotation] Fetching existing CA certificate...")
	existingCA, err := getSecretVersion(ctx, projectID, "private-llm-ca-cert", "latest")
	if err != nil {
		return fmt.Errorf("failed to fetch existing CA: %w", err)
	}

	log.Printf("[rotation] Parsing existing CA...")
	block, _ := pem.Decode(existingCA)
	if block == nil {
		return fmt.Errorf("failed to decode CA PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA cert: %w", err)
	}

	log.Printf("[rotation] Fetching CA private key...")
	caKeyPEM, err := getSecretVersion(ctx, projectID, "private-llm-ca-key", "latest")
	if err != nil {
		return fmt.Errorf("failed to fetch CA key: %w", err)
	}

	log.Printf("[rotation] Generating new server certificate (1-week validity)...")
	serverCert, serverKey, serverNotAfter, err := generateServerCert(existingCA, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate server cert: %w", err)
	}

	log.Printf("[rotation] Generating new client certificate (1-week validity)...")
	clientCert, clientKey, clientNotAfter, err := generateClientCert(existingCA, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate client cert: %w", err)
	}

	log.Printf("[rotation] Generating new internal token...")
	internalToken, err := generateToken(64)
	if err != nil {
		return fmt.Errorf("failed to generate internal token: %w", err)
	}

	creds := &Credentials{
		CACert:         existingCA,
		ServerCert:     serverCert,
		ServerKey:      serverKey,
		ClientCert:     clientCert,
		ClientKey:      clientKey,
		InternalToken:  internalToken,
		ServerNotAfter: serverNotAfter,
		ClientNotAfter: clientNotAfter,
	}

	log.Printf("[rotation] Validating new credentials...")
	if err := validateCredentials(creds); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	if dryRun {
		log.Printf("[rotation] Dry run mode - would rotate 5 secrets")
		log.Printf("[rotation] CA expires: %s", caCert.NotAfter.Format("2006-01-02"))
		log.Printf("[rotation] Server expires: %s", creds.ServerNotAfter.Format("2006-01-02"))
		log.Printf("[rotation] Client expires: %s", creds.ClientNotAfter.Format("2006-01-02"))
		return nil
	}

	log.Printf("[rotation] Creating new secret versions...")
	if err := createSecretVersion(ctx, projectID, "private-llm-server-cert", creds.ServerCert); err != nil {
		return fmt.Errorf("failed to create server-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-key", creds.ServerKey); err != nil {
		return fmt.Errorf("failed to create server-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-cert", creds.ClientCert); err != nil {
		return fmt.Errorf("failed to create client-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-key", creds.ClientKey); err != nil {
		return fmt.Errorf("failed to create client-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-internal-token", []byte(creds.InternalToken)); err != nil {
		return fmt.Errorf("failed to create internal-token version: %w", err)
	}

	log.Printf("[rotation] Redeploying proxy function to pick up new client cert...")
	if err := redeployProxyFunction(ctx); err != nil {
		log.Printf("[rotation] Warning: failed to redeploy proxy function: %v", err)
		// Don't fail the rotation, just warn
	}

	log.Printf("[rotation] Rotation complete")
	log.Printf("[rotation] CA expires: %s", caCert.NotAfter.Format("2006-01-02"))
	log.Printf("[rotation] Server expires: %s", creds.ServerNotAfter.Format("2006-01-02"))
	log.Printf("[rotation] Client expires: %s", creds.ClientNotAfter.Format("2006-01-02"))
	return nil
}

// handleRotation rotates server/client certs and internal token (keeps CA) - HTTP version
func handleRotation(ctx context.Context, w http.ResponseWriter, dryRun bool) error {
	projectID := os.Getenv("GCP_PROJECT")

	log.Printf("[rotation] Fetching existing CA certificate...")
	existingCA, err := getSecretVersion(ctx, projectID, "private-llm-ca-cert", "latest")
	if err != nil {
		return fmt.Errorf("failed to fetch existing CA: %w", err)
	}

	log.Printf("[rotation] Parsing existing CA...")
	block, _ := pem.Decode(existingCA)
	if block == nil {
		return fmt.Errorf("failed to decode CA PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// For rotation, we need the CA key to sign new certs
	log.Printf("[rotation] Fetching CA private key...")
	caKeyPEM, err := getSecretVersion(ctx, projectID, "private-llm-ca-key", "latest")
	if err != nil {
		return fmt.Errorf("failed to fetch CA key: %w", err)
	}

	log.Printf("[rotation] Generating new server certificate (1-week validity)...")
	serverCert, serverKey, serverNotAfter, err := generateServerCert(existingCA, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate server cert: %w", err)
	}

	log.Printf("[rotation] Generating new client certificate (1-week validity)...")
	clientCert, clientKey, clientNotAfter, err := generateClientCert(existingCA, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to generate client cert: %w", err)
	}

	log.Printf("[rotation] Generating new internal token...")
	internalToken, err := generateToken(64)
	if err != nil {
		return fmt.Errorf("failed to generate internal token: %w", err)
	}

	creds := &Credentials{
		CACert:         existingCA,
		ServerCert:     serverCert,
		ServerKey:      serverKey,
		ClientCert:     clientCert,
		ClientKey:      clientKey,
		InternalToken:  internalToken,
		ServerNotAfter: serverNotAfter,
		ClientNotAfter: clientNotAfter,
	}

	log.Printf("[rotation] Validating new credentials...")
	if err := validateCredentials(creds); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	if dryRun {
		log.Printf("[rotation] Dry run mode - would rotate 5 secrets")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "dry_run",
			"message": "Would rotate server cert, client cert, and internal token",
			"credentials": map[string]string{
				"ca_expires":     caCert.NotAfter.Format("2006-01-02"),
				"server_expires": creds.ServerNotAfter.Format("2006-01-02"),
				"client_expires": creds.ClientNotAfter.Format("2006-01-02"),
			},
		})
		return nil
	}

	log.Printf("[rotation] Creating new secret versions...")
	if err := createSecretVersion(ctx, projectID, "private-llm-server-cert", creds.ServerCert); err != nil {
		return fmt.Errorf("failed to create server-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-server-key", creds.ServerKey); err != nil {
		return fmt.Errorf("failed to create server-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-cert", creds.ClientCert); err != nil {
		return fmt.Errorf("failed to create client-cert version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-client-key", creds.ClientKey); err != nil {
		return fmt.Errorf("failed to create client-key version: %w", err)
	}
	if err := createSecretVersion(ctx, projectID, "private-llm-internal-token", []byte(creds.InternalToken)); err != nil {
		return fmt.Errorf("failed to create internal-token version: %w", err)
	}

	log.Printf("[rotation] Redeploying proxy function to pick up new client cert...")
	if err := redeployProxyFunction(ctx); err != nil {
		log.Printf("[rotation] Warning: failed to redeploy proxy function: %v", err)
		// Don't fail the rotation, just warn
	}

	log.Printf("[rotation] Rotation complete")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Credentials rotated successfully",
		"credentials": map[string]string{
			"ca_expires":     caCert.NotAfter.Format("2006-01-02"),
			"server_expires": creds.ServerNotAfter.Format("2006-01-02"),
			"client_expires": creds.ClientNotAfter.Format("2006-01-02"),
		},
	})
	return nil
}

// checkRotationEligibility determines if rotation is safe to perform
func checkRotationEligibility(ctx context.Context) (bool, string, error) {
	projectID := os.Getenv("GCP_PROJECT")
	zone := os.Getenv("GCP_ZONE")
	vmName := os.Getenv("VM_NAME")

	// Check VM status only if VM_NAME is configured
	if vmName != "" {
		computeClient, err := compute.NewInstancesRESTClient(ctx)
		if err != nil {
			return false, "", fmt.Errorf("failed to create compute client: %w", err)
		}
		defer computeClient.Close()

		instance, err := computeClient.Get(ctx, &computepb.GetInstanceRequest{
			Project:  projectID,
			Zone:     zone,
			Instance: vmName,
		})
		if err != nil {
			return false, "", fmt.Errorf("failed to get VM instance: %w", err)
		}

		vmStatus := instance.GetStatus()
		if vmStatus != "STOPPED" && vmStatus != "TERMINATED" {
			return false, fmt.Sprintf("VM is %s (must be STOPPED or TERMINATED)", vmStatus), nil
		}
		log.Printf("[rotation] VM %s is stopped", vmName)
	} else {
		log.Printf("[rotation] VM_NAME not configured, skipping VM status check")
	}

	// Check certificate expiration
	serverCert, err := getSecretVersion(ctx, projectID, "private-llm-server-cert", "latest")
	if err != nil {
		return false, "", fmt.Errorf("failed to fetch server cert: %w", err)
	}

	block, _ := pem.Decode(serverCert)
	if block == nil {
		return false, "", fmt.Errorf("failed to decode server cert PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse server cert: %w", err)
	}

	hoursUntilExpiry := time.Until(cert.NotAfter).Hours()
	if hoursUntilExpiry > 24 {
		return false, fmt.Sprintf("Certificate expires in %.1f hours (rotation triggers at 24 hours)", hoursUntilExpiry), nil
	}

	log.Printf("[rotation] Eligible for rotation: cert expires in %.1f hours", hoursUntilExpiry)
	return true, "", nil
}

// generateCA creates a new self-signed CA certificate (10-year validity)
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

// generateServerCert creates a new server certificate signed by the CA
func generateServerCert(caCertPEM, caKeyPEM []byte) ([]byte, []byte, time.Time, error) {
	// Parse CA cert
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// Parse CA key
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA key: %w", err)
	}

	// Generate server key
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

// generateClientCert creates a new client certificate signed by the CA
func generateClientCert(caCertPEM, caKeyPEM []byte) ([]byte, []byte, time.Time, error) {
	// Parse CA cert
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// Parse CA key
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CA key: %w", err)
	}

	// Generate client key
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

// generateToken creates a cryptographically random token
func generateToken(length int) (string, error) {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// validateCredentials ensures all generated credentials are valid
func validateCredentials(creds *Credentials) error {
	// Parse CA cert
	caBlock, _ := pem.Decode(creds.CACert)
	if caBlock == nil {
		return fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// Validate server cert chain
	serverBlock, _ := pem.Decode(creds.ServerCert)
	if serverBlock == nil {
		return fmt.Errorf("failed to decode server cert PEM")
	}
	serverCert, err := x509.ParseCertificate(serverBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse server cert: %w", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := serverCert.Verify(opts); err != nil {
		return fmt.Errorf("server cert verification failed: %w", err)
	}

	// Validate client cert chain
	clientBlock, _ := pem.Decode(creds.ClientCert)
	if clientBlock == nil {
		return fmt.Errorf("failed to decode client cert PEM")
	}
	clientCert, err := x509.ParseCertificate(clientBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse client cert: %w", err)
	}

	opts.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if _, err := clientCert.Verify(opts); err != nil {
		return fmt.Errorf("client cert verification failed: %w", err)
	}

	// Test in-memory TLS handshake
	serverKeyBlock, _ := pem.Decode(creds.ServerKey)
	if serverKeyBlock == nil {
		return fmt.Errorf("failed to decode server key PEM")
	}
	clientKeyBlock, _ := pem.Decode(creds.ClientKey)
	if clientKeyBlock == nil {
		return fmt.Errorf("failed to decode client key PEM")
	}

	_, err = tls.X509KeyPair(creds.ServerCert, creds.ServerKey)
	if err != nil {
		return fmt.Errorf("server cert/key pair invalid: %w", err)
	}

	_, err = tls.X509KeyPair(creds.ClientCert, creds.ClientKey)
	if err != nil {
		return fmt.Errorf("client cert/key pair invalid: %w", err)
	}

	log.Printf("[rotation] Credentials validated successfully")
	return nil
}

// getSecretVersion retrieves a secret version from Secret Manager
func getSecretVersion(ctx context.Context, projectID, secretID, version string) ([]byte, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/%s", projectID, secretID, version),
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to access secret version: %w", err)
	}

	return result.Payload.Data, nil
}

// createSecretVersion adds a new version to an existing secret
func createSecretVersion(ctx context.Context, projectID, secretID string, data []byte) error {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AddSecretVersionRequest{
		Parent: fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID),
		Payload: &secretmanagerpb.SecretPayload{
			Data: data,
		},
	}

	_, err = client.AddSecretVersion(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to add secret version: %w", err)
	}

	log.Printf("[rotation] Created new version for secret: %s", secretID)
	return nil
}

// redeployProxyFunction forces the proxy function to redeploy and pick up new secrets
func redeployProxyFunction(ctx context.Context) error {
	projectID := os.Getenv("GCP_PROJECT")
	region := os.Getenv("GCP_REGION")
	if region == "" {
		region = "us-central1"
	}
	functionName := os.Getenv("FUNCTION_NAME")
	if functionName == "" {
		functionName = "private-llm-proxy"
	}

	runService, err := run.NewService(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Cloud Run client: %w", err)
	}

	serviceName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, functionName)

	// Get current service
	service, err := runService.Projects.Locations.Services.Get(serviceName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}

	// Force a new revision by updating TEMPLATE annotations (not service-level)
	// Cloud Run only creates new revisions when the template changes
	if service.Template.Annotations == nil {
		service.Template.Annotations = make(map[string]string)
	}
	service.Template.Annotations["rotation-timestamp"] = time.Now().Format(time.RFC3339)

	// Update the service template
	_, err = runService.Projects.Locations.Services.Patch(serviceName, service).UpdateMask("template.annotations").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	log.Printf("[rotation] Triggered redeploy of proxy function")
	return nil
}
