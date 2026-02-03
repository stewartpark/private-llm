package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestTokenValidation(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		expectError bool
		description string
	}{
		{
			name:        "valid token",
			token:       "abcd1234efgh5678ijkl9012mnop3456",
			expectError: false,
			description: "should accept valid token",
		},
		{
			name:        "empty token",
			token:       "",
			expectError: true,
			description: "should reject empty token",
		},
		{
			name:        "short token",
			token:       "short",
			expectError: true,
			description: "should reject short token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToken(tt.token)
			if (err != nil) != tt.expectError {
				t.Errorf("%s: expected error: %v, got error: %v",
					tt.description, tt.expectError, err)
			}
		})
	}
}

func TestTimeoutConversion(t *testing.T) {
	// Test normal timeout
	normalTimeout := timeoutFromEnv("300")
	if normalTimeout != 300*time.Second {
		t.Errorf("Expected 300s timeout, got %v", normalTimeout)
	}

	// Test provisioning timeout
	os.Setenv("PROVISIONING_IDLE_TIMEOUT", "1800")
	defer os.Unsetenv("PROVISIONING_IDLE_TIMEOUT")
	provisioningTimeout := timeoutFromEnv("1800")
	if provisioningTimeout != 1800*time.Second {
		t.Errorf("Expected 1800s timeout, got %v", provisioningTimeout)
	}
}

func TestRetryLogic(t *testing.T) {
	expectedRetries := 12
	if maxRetries != expectedRetries {
		t.Errorf("Expected %d retries, got %d", expectedRetries, maxRetries)
	}
}

func TestEnvironmentVariables(t *testing.T) {
	// Test that all expected environment variables are checked
	envVars := []string{"GCP_PROJECT", "GOOGLE_CLOUD_PROJECT", "GOOGLE_APPLICATION_CREDENTIALS"}

	for _, envVar := range envVars {
		if os.Getenv(envVar) == "" {
			t.Logf("Note: Environment variable %s not set", envVar)
		}
	}
}

func TestFirestoreDocumentPath(t *testing.T) {
	// Test Firestore document path format without actual call
	projectID := "test-project"
	databaseID := "private-llm"
	vmName := "test-vm"

	expectedPath := fmt.Sprintf("projects/%s/databases/%s/documents/vm_state/%s",
		projectID, databaseID, vmName)

	if expectedPath != getFirestoreDocumentPath(projectID, databaseID, vmName) {
		t.Errorf("Firestore document path mismatch")
	}
}

// Benchmark tests
func BenchmarkTokenValidation(b *testing.B) {
	token := "abcd1234efgh5678ijkl9012mnop3456"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validateToken(token)
	}
}

func BenchmarkFirestoreUpdate(b *testing.B) {
	ctx := context.Background()
	client := &firestore.Client{} // Mock client
	vmName := "test-vm"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updateLastRequest(ctx, client, vmName, time.Now().Unix())
	}
}
