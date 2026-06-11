package s3

import (
	"context"
	"testing"
)

// TestNewSmoke verifies New constructs without panicking and
// normalizes the prefix; live S3 calls are exercised only in the
// integrated review. LoadDefaultConfig rarely errors (it tolerates
// absent credentials until a call is made).
func TestNewSmoke(t *testing.T) {
	b, err := New(context.Background(), "test-bucket", "ir-hub")
	if err != nil {
		t.Skipf("aws config load failed in this env: %v", err)
	}
	if b.prefix != "ir-hub/" {
		t.Errorf("prefix = %q, want trailing slash added", b.prefix)
	}
	if b.Name() != "s3" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestNewEmptyPrefix(t *testing.T) {
	b, err := New(context.Background(), "test-bucket", "")
	if err != nil {
		t.Skipf("aws config load failed: %v", err)
	}
	if b.prefix != "" {
		t.Errorf("prefix = %q, want empty", b.prefix)
	}
}
