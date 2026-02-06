package function

import (
	"testing"
)

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
