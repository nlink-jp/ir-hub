package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func tempDB(t *testing.T) *Store {
	t.Helper()
	fixed := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "test.db"),
		WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSchemaVersion(t *testing.T) {
	s := tempDB(t)
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != "1" {
		t.Errorf("SchemaVersion = %q, want 1", v)
	}
}

func TestCaseLifecycle(t *testing.T) {
	s := tempDB(t)

	c, err := s.CreateCase("DB outage", "high", "private", "U001")
	if err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	if c.ID != 1 || c.State != StateCreating || c.ChannelID != "" {
		t.Errorf("created case = %+v, want id=1 state=creating", c)
	}
	if c.OpenedAt.IsZero() {
		t.Error("OpenedAt is zero")
	}

	if err := s.ActivateCase(c.ID, "C123", "ir-0001-db-outage"); err != nil {
		t.Fatalf("ActivateCase: %v", err)
	}
	got, err := s.CaseByChannel("C123")
	if err != nil {
		t.Fatalf("CaseByChannel: %v", err)
	}
	if got.State != StateOpen || got.ChannelName != "ir-0001-db-outage" {
		t.Errorf("activated case = %+v", got)
	}

	// Sequence numbers keep counting.
	c2, err := s.CreateCase("Phishing", "medium", "public", "U002")
	if err != nil {
		t.Fatalf("CreateCase 2: %v", err)
	}
	if c2.ID != 2 {
		t.Errorf("second case id = %d, want 2", c2.ID)
	}

	if err := s.CloseCase(c.ID, "U003"); err != nil {
		t.Fatalf("CloseCase: %v", err)
	}
	got, _ = s.CaseByID(c.ID)
	if got.State != StateClosed || got.ClosedBy != "U003" || got.ClosedAt.IsZero() {
		t.Errorf("closed case = %+v", got)
	}

	// Closing again fails with ErrNotOpen.
	if err := s.CloseCase(c.ID, "U003"); !errors.Is(err, ErrNotOpen) {
		t.Errorf("double close err = %v, want ErrNotOpen", err)
	}
}

func TestFailCase(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("t", "low", "public", "U001")
	if err := s.FailCase(c.ID); err != nil {
		t.Fatalf("FailCase: %v", err)
	}
	got, _ := s.CaseByID(c.ID)
	if got.State != StateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	// Failing a non-creating case errors.
	if err := s.FailCase(c.ID); err == nil {
		t.Error("FailCase on failed case: want error")
	}
}

func TestCaseNotFound(t *testing.T) {
	s := tempDB(t)
	if _, err := s.CaseByID(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("CaseByID(99) err = %v, want ErrNotFound", err)
	}
	if _, err := s.CaseByChannel("CNOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("CaseByChannel err = %v, want ErrNotFound", err)
	}
}

func TestListOpenCases(t *testing.T) {
	s := tempDB(t)
	c1, _ := s.CreateCase("a", "low", "public", "U1")
	s.ActivateCase(c1.ID, "C1", "ir-0001-a")
	c2, _ := s.CreateCase("b", "low", "public", "U1") // stays creating
	_ = c2
	c3, _ := s.CreateCase("c", "low", "public", "U1")
	s.ActivateCase(c3.ID, "C3", "ir-0003-c")
	s.CloseCase(c3.ID, "U1")

	open, err := s.ListOpenCases()
	if err != nil {
		t.Fatalf("ListOpenCases: %v", err)
	}
	if len(open) != 1 || open[0].ChannelID != "C1" {
		t.Errorf("open cases = %+v, want only C1", open)
	}
}

func TestInsertMessageDedup(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "low", "public", "U1")
	s.ActivateCase(c.ID, "C1", "ir-0001-a")

	m := Message{ChannelID: "C1", TS: "1718000000.000100", CaseID: c.ID,
		UserID: "U1", Text: "hello", Raw: `{"text":"hello"}`, Source: SourceEvent}
	ins, err := s.InsertMessage(m)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if !ins {
		t.Error("first insert: inserted = false, want true")
	}

	// Same (channel, ts) from backfill is ignored.
	m.Source = SourceBackfill
	ins, err = s.InsertMessage(m)
	if err != nil {
		t.Fatalf("InsertMessage dup: %v", err)
	}
	if ins {
		t.Error("duplicate insert: inserted = true, want false")
	}

	n, _ := s.CountMessages(c.ID)
	if n != 1 {
		t.Errorf("CountMessages = %d, want 1", n)
	}
}

func TestMaxMessageTS(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "low", "public", "U1")
	s.ActivateCase(c.ID, "C1", "ir-0001-a")

	ts, err := s.MaxMessageTS("C1")
	if err != nil {
		t.Fatalf("MaxMessageTS empty: %v", err)
	}
	if ts != "" {
		t.Errorf("MaxMessageTS on empty = %q, want \"\"", ts)
	}

	for _, v := range []string{"1718000000.000100", "1718000002.000100", "1718000001.000100"} {
		s.InsertMessage(Message{ChannelID: "C1", TS: v, CaseID: c.ID, Raw: "{}", Source: SourceEvent})
	}
	ts, _ = s.MaxMessageTS("C1")
	if ts != "1718000002.000100" {
		t.Errorf("MaxMessageTS = %q, want newest", ts)
	}
}

func TestInsertDenial(t *testing.T) {
	s := tempDB(t)
	err := s.InsertDenial(Denial{UserID: "U9", ChannelID: "C9",
		Entrypoint: "slash", Action: "new", Reason: "not in allow list"})
	if err != nil {
		t.Fatalf("InsertDenial: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM acl_denials`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("denials = %d, want 1", n)
	}
}

func TestReopenDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c, _ := s.CreateCase("persist", "low", "public", "U1")
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.CaseByID(c.ID)
	if err != nil {
		t.Fatalf("CaseByID after reopen: %v", err)
	}
	if got.Title != "persist" {
		t.Errorf("title = %q, want persist", got.Title)
	}
}
