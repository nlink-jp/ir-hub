package gcs

import (
	"context"
	"testing"
)

// TestNewSmoke documents that New may succeed (ADC client
// construction is lazy) or fail depending on the host's credential
// state; either way it must not panic.
func TestNewSmoke(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent")
	if b, err := New(context.Background(), "test-bucket"); err == nil {
		b.Close()
	}
}
