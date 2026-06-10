package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestServeFailsFastWithoutTokens verifies that serve refuses to
// start (with a clear message) when Slack tokens are absent —
// before any network activity.
func TestServeFailsFastWithoutTokens(t *testing.T) {
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(key, "IRHUB_") {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"serve"})

	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "IRHUB_SLACK_APP_TOKEN") {
		t.Fatalf("serve without tokens: err = %v, want app-token error", err)
	}
}
