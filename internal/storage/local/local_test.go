package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	b := New(dir)
	if b.Name() != "local" {
		t.Errorf("Name = %q", b.Name())
	}

	content := []byte("# postmortem\n")
	if err := b.Write(context.Background(), "knowledge/tac-20260601-001-x.md", content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "knowledge", "tac-20260601-001-x.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q", got)
	}
}

func TestWriteCreatesNestedDirs(t *testing.T) {
	dir := t.TempDir()
	b := New(dir)
	if err := b.Write(context.Background(), "a/b/c/file.json", []byte("{}")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b", "c", "file.json")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
}
