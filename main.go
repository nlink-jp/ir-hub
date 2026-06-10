package main

import "github.com/nlink-jp/ir-hub/cmd"

// version is set at build time via -ldflags "-X main.version=...".
// Cobra reads it via cmd.Execute and surfaces it through
// `ir-hub --version`.
var version = "dev"

func main() {
	cmd.Execute(version)
}
