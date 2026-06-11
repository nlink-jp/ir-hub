// Package storage writes knowledge-document bytes to a pluggable
// backend — local filesystem, GCS, or S3 — selected by config. It
// is deliberately minimal (write-only, []byte payloads): ir-hub
// exports small JSON/Markdown documents, and other teams read the
// storage directly (RFP: "a deliberately simple integration
// structure").
package storage

import (
	"context"
	"fmt"

	"github.com/nlink-jp/ir-hub/internal/config"
	"github.com/nlink-jp/ir-hub/internal/storage/local"
)

// Backend stores document bytes at forward-slash-separated paths.
type Backend interface {
	// Write stores content at path (the caller pre-sanitizes the
	// path; backends must not allow traversal above their root).
	Write(ctx context.Context, path string, content []byte) error
	// Name returns the backend identifier (local | gcs | s3).
	Name() string
}

// New constructs the backend named by cfg.Backend. A cloud backend
// whose client cannot be created (missing credentials, off-cloud)
// returns an error so the caller can degrade gracefully rather than
// crashing the bot. The gcs and s3 cases are wired in their own
// commits.
func New(ctx context.Context, cfg config.StorageConfig) (Backend, error) {
	switch cfg.Backend {
	case "local":
		return local.New(cfg.LocalPath), nil
	default:
		return nil, fmt.Errorf("storage: backend %q not available", cfg.Backend)
	}
}
