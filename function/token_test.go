package function

import (
	"testing"
)

func TestGenerateToken(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{
			name:   "32 char token",
			length: 32,
		},
		{
			name:   "64 char token",
			length: 64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := generateToken(tt.length)
			if err != nil {
				t.Errorf("generateToken(%d) returned error: %v", tt.length, err)
			}
			if len(token) != tt.length {
				t.Errorf("generateToken(%d) returned token of length %d, want %d", tt.length, len(token), tt.length)
			}
		})
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	// Generate multiple tokens and ensure they're unique
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := generateToken(64)
		if err != nil {
			t.Fatalf("generateToken failed: %v", err)
		}
		if tokens[token] {
			t.Error("generateToken produced duplicate token")
		}
		tokens[token] = true
	}
}

func TestCredentialsStruct(t *testing.T) {
	creds := &Credentials{
		CACert:        []byte("ca-cert"),
		CAKey:         []byte("ca-key"),
		ServerCert:    []byte("server-cert"),
		ServerKey:     []byte("server-key"),
		ClientCert:    []byte("client-cert"),
		ClientKey:     []byte("client-key"),
		InternalToken: "test-token",
	}

	if string(creds.CACert) != "ca-cert" {
		t.Error("CACert not set correctly")
	}
	if creds.InternalToken != "test-token" {
		t.Error("InternalToken not set correctly")
	}
}

func TestRotationConfigStruct(t *testing.T) {
	config := RotationConfig{
		RotateCA: true,
		DryRun:   true,
		Force:    false,
	}

	if !config.RotateCA {
		t.Error("RotateCA should be true")
	}
	if !config.DryRun {
		t.Error("DryRun should be true")
	}
	if config.Force {
		t.Error("Force should be false")
	}
}
