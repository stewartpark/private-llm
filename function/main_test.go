package function

import (
	"testing"
)

func TestSecureCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "equal strings",
			a:    "abcd1234efgh5678ijkl9012mnop3456",
			b:    "abcd1234efgh5678ijkl9012mnop3456",
			want: true,
		},
		{
			name: "different strings",
			a:    "abcd1234efgh5678ijkl9012mnop3456",
			b:    "different-token-value-here123456",
			want: false,
		},
		{
			name: "empty strings",
			a:    "",
			b:    "",
			want: true,
		},
		{
			name: "one empty",
			a:    "token",
			b:    "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secureCompare(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("secureCompare(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestVMStateStruct(t *testing.T) {
	// Test VMState struct initialization
	state := VMState{
		LastRequestUnix: 1234567890,
		Provisioned:     true,
	}

	if state.LastRequestUnix != 1234567890 {
		t.Errorf("Expected LastRequestUnix 1234567890, got %d", state.LastRequestUnix)
	}
	if !state.Provisioned {
		t.Error("Expected Provisioned to be true")
	}
}

func TestProvisioningIdleTimeout(t *testing.T) {
	// Test that provisioning idle timeout is 30 minutes
	expectedTimeout := 1800
	if provisioningIdleTimeout != expectedTimeout {
		t.Errorf("Expected provisioningIdleTimeout %d, got %d", expectedTimeout, provisioningIdleTimeout)
	}
}
