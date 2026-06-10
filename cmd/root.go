// Package cmd defines the ir-hub CLI surface via cobra.
//
// ir-hub is a resident Slack bot (Socket Mode) that supports the
// full incident-response lifecycle: case channel creation, in-flight
// response support, postmortem analysis, and knowledge accumulation
// and reuse — all backed by Vertex AI Gemini.
//
// See the approved RFP under docs/ja/ir-hub-rfp.ja.md (Japanese,
// canonical) / docs/en/ir-hub-rfp.md for the full design rationale.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ir-hub",
	Short: "Incident-response lifecycle hub — Slack ChatOps bot",
	Long: `ir-hub is a one-package incident-response lifecycle hub for
Slack ChatOps. It runs as a resident Socket Mode bot that opens a
dedicated channel per case, supports the response while it is in
flight, runs an LLM postmortem on close, and accumulates the
extracted knowledge for reuse on future incidents.

Subcommands (serve, export, ...) arrive with Phase 1 development.
For design rationale see docs/en/ir-hub-rfp.md (the project RFP).`,
}

// Execute runs the root command. Called from main.go with the
// build-time version string injected via -ldflags.
func Execute(version string) {
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
