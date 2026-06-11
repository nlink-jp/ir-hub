package export

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/ir-hub/internal/storage/storagetest"
	"github.com/nlink-jp/ir-hub/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	fixed := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"),
		store.WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seed(t *testing.T, st *store.Store, caseID int64, titles ...string) {
	t.Helper()
	runID, err := st.BeginPMRun(caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinalizePMRun(runID, caseID, "{}", "# r", len(titles),
		func(i int, tacticID string) store.KnowledgeRow {
			return store.KnowledgeRow{
				TacticID: tacticID, Title: titles[i], Category: "log-analysis",
				Confidence: "confirmed", TagsJSON: `["x"]`, Summary: "s",
				DocJSON: `{"id":"` + tacticID + `"}`, DocMD: "# " + titles[i],
			}
		}); err != nil {
		t.Fatal(err)
	}
}

func TestExportAll(t *testing.T) {
	st := newStore(t)
	c1, _ := st.CreateCase("a", "high", "private", "U1")
	c2, _ := st.CreateCase("b", "low", "public", "U1")
	seed(t, st, c1.ID, "Check systemd logs")
	seed(t, st, c2.ID, "Review auth log")

	fake := storagetest.New()
	svc := New(st, fake, WithLogger(t.Logf))

	n, err := svc.ExportAll(context.Background())
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if n != 2 {
		t.Errorf("written = %d, want 2", n)
	}
	// Deterministic paths: tactic_id + slug, JSON + MD pair.
	got := fake.Get("tac-20260610-001-check-systemd-logs.md")
	if string(got) != "# Check systemd logs" {
		t.Errorf("md content = %q", got)
	}
	if j := fake.Get("tac-20260610-001-check-systemd-logs.json"); j == nil {
		t.Error("json pair missing")
	}
	if len(fake.Paths()) != 4 { // 2 docs × (json + md)
		t.Errorf("paths = %d, want 4", len(fake.Paths()))
	}
}

func TestExportCase(t *testing.T) {
	st := newStore(t)
	c1, _ := st.CreateCase("a", "high", "private", "U1")
	c2, _ := st.CreateCase("b", "low", "public", "U1")
	seed(t, st, c1.ID, "tactic one", "tactic two")
	seed(t, st, c2.ID, "other case tactic")

	fake := storagetest.New()
	svc := New(st, fake, WithLogger(t.Logf))

	n, err := svc.ExportCase(context.Background(), c1.ID)
	if err != nil {
		t.Fatalf("ExportCase: %v", err)
	}
	if n != 2 {
		t.Errorf("written = %d, want 2 (only c1)", n)
	}
	for _, p := range fake.Paths() {
		if filepath.Base(p) == "other-case-tactic.md" {
			t.Errorf("c2 doc leaked: %s", p)
		}
	}
}

func TestExportBestEffort(t *testing.T) {
	st := newStore(t)
	c, _ := st.CreateCase("a", "high", "private", "U1")
	seed(t, st, c.ID, "t1")

	fake := storagetest.New()
	fake.Err = errors.New("backend down")
	svc := New(st, fake, WithLogger(t.Logf))

	n, err := svc.ExportAll(context.Background())
	if n != 0 || err == nil {
		t.Errorf("written = %d, err = %v, want 0 + error", n, err)
	}
}

func TestExportEmpty(t *testing.T) {
	st := newStore(t)
	fake := storagetest.New()
	svc := New(st, fake, WithLogger(t.Logf))
	n, err := svc.ExportAll(context.Background())
	if n != 0 || err != nil {
		t.Errorf("empty export: n=%d err=%v", n, err)
	}
}
