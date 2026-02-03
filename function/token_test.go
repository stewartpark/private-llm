package function

import (
	"testing"
	"time"
)

func TestTimeoutFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{
			name: "normal timeout",
			env:  "300",
			want: 300 * time.Second,
		},
		{
			name: "provisioning timeout",
			env:  "1800",
			want: 1800 * time.Second,
		},
		{
			name: "zero timeout",
			env:  "0",
			want: 0 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeoutFromEnv(tt.env)
			if got != tt.want {
				t.Errorf("timeoutFromEnv(%q) = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestNormalizeToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "valid token",
			token: "abcd1234efgh5678ijkl9012mnop3456",
			want:  "abcd1234efgh5678ijkl9012mnop3456",
		},
		{
			name:  "upper case token",
			token: "ABCd1234EFgh5678ijkl9012MnOp3456",
			want:  "abcD1234Efgh5678ijkl9012mNoP3456",
		},
		{
			name:  "short token",
			token: "short",
			want:  "",
		},
		{
			name:  "empty token",
			token: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeToken(tt.token)
			if got != tt.want {
				t.Errorf("normalizeToken(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

func TestRetryExponentialBackoff(t *testing.T) {
	delay := retryDelay()
	if delay < 0 || delay > 5*time.Second {
		t.Errorf("retryDelay() = %v, expected 0 < delay < 5s", delay)
	}
}

func TestFirestoreUpdate(t *testing.T) {
	// This is a placeholder test
	// Real test would require a firestore mock

	t.Run("should not panic", func(t *testing.T) {
		defer func() {
			r := recover()
			if r != nil {
				t.Errorf("unexpected panic: %v", r)
			}
		}()

		// Mock firestore client
		// client := &firestore.Client{}
		// updateLastRequest(context.Background(), client, "test-vm", time.Now().Unix())
	})
}
