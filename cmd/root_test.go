package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	rootCmd.Version = "v9.9.9-test"
	defer func() { rootCmd.Version = "" }()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "v9.9.9-test") {
		t.Errorf("--version output = %q, want it to contain %q", got, "v9.9.9-test")
	}
}

func TestRootCommandMetadata(t *testing.T) {
	if rootCmd.Use != "ir-hub" {
		t.Errorf("rootCmd.Use = %q, want %q", rootCmd.Use, "ir-hub")
	}
	if rootCmd.Short == "" {
		t.Error("rootCmd.Short must not be empty")
	}
}
