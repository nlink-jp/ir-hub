// Package local is the filesystem storage backend: it writes
// documents under a root directory, creating parent directories as
// needed.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Backend writes under Root.
type Backend struct {
	root string
}

// New creates a local backend rooted at dir.
func New(dir string) *Backend { return &Backend{root: dir} }

func (b *Backend) Name() string { return "local" }

// Write stores content at root/path, creating parent directories.
func (b *Backend) Write(ctx context.Context, path string, content []byte) error {
	full := filepath.Join(b.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("local: mkdir for %s: %w", path, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return fmt.Errorf("local: write %s: %w", path, err)
	}
	return nil
}
