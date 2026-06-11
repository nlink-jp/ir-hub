// Package storagetest provides an in-memory storage.Backend for
// tests of the export service and the bot.
package storagetest

import (
	"context"
	"sync"
)

// Fake records every write in Writes. Set Err to make Write fail.
type Fake struct {
	mu     sync.Mutex
	Writes map[string][]byte
	Err    error
}

// New returns an empty Fake.
func New() *Fake { return &Fake{Writes: map[string][]byte{}} }

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Write(ctx context.Context, path string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	f.Writes[path] = cp
	return nil
}

// Paths returns the written paths (unsorted snapshot).
func (f *Fake) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Writes))
	for p := range f.Writes {
		out = append(out, p)
	}
	return out
}

// Get returns the content written at path (nil if absent).
func (f *Fake) Get(path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Writes[path]
}
