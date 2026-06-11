package store

import (
	"database/sql"
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


// TestMigrateFromV1 builds a database the way v0.1.0 did (v1 DDL,
// schema_version='1', existing data) and verifies Open migrates it
// to the latest version preserving data.
func TestMigrateFromV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.db")

	// Hand-build a v1 database.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v1stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '1')`,
		`CREATE TABLE cases (
			id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
			severity TEXT NOT NULL, visibility TEXT NOT NULL,
			channel_id TEXT UNIQUE, channel_name TEXT,
			state TEXT NOT NULL DEFAULT 'creating',
			opened_by TEXT NOT NULL, opened_at TEXT NOT NULL,
			closed_by TEXT, closed_at TEXT)`,
		`INSERT INTO cases (title, severity, visibility, channel_id, channel_name, state, opened_by, opened_at)
		 VALUES ('legacy', 'high', 'private', 'C1', 'ir-0001-legacy', 'open', 'U1', '2026-06-01T00:00:00Z')`,
		`CREATE TABLE messages (
			channel_id TEXT NOT NULL, ts TEXT NOT NULL, case_id INTEGER NOT NULL,
			thread_ts TEXT, user_id TEXT, bot_id TEXT, subtype TEXT,
			text TEXT NOT NULL DEFAULT '', raw TEXT NOT NULL,
			source TEXT NOT NULL, ingested_at TEXT NOT NULL,
			PRIMARY KEY (channel_id, ts))`,
		`INSERT INTO messages (channel_id, ts, case_id, text, raw, source, ingested_at)
		 VALUES ('C1', '1718000000.000001', 1, 'hello', '{}', 'event', '2026-06-01T00:00:00Z')`,
		`CREATE TABLE acl_denials (
			id INTEGER PRIMARY KEY AUTOINCREMENT, denied_at TEXT NOT NULL,
			user_id TEXT NOT NULL, channel_id TEXT, entrypoint TEXT NOT NULL,
			action TEXT, reason TEXT NOT NULL)`,
	}
	for _, stmt := range v1stmts {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("v1 fixture: %v", err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open v1 db: %v", err)
	}
	defer s.Close()

	if v, _ := s.SchemaVersion(); v != "3" {
		t.Errorf("migrated version = %q, want 3", v)
	}
	// Existing data preserved.
	c, err := s.CaseByChannel("C1")
	if err != nil || c.Title != "legacy" {
		t.Errorf("legacy case = %+v, err %v", c, err)
	}
	if n, _ := s.CountMessages(1); n != 1 {
		t.Errorf("legacy messages = %d, want 1", n)
	}
	// v2 tables usable.
	if _, err := s.BeginPMRun(c.ID); err != nil {
		t.Errorf("BeginPMRun on migrated db: %v", err)
	}
}

func TestListMessagesOrder(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "low", "public", "U1")
	s.ActivateCase(c.ID, "C1", "ir-0001-a")
	for _, ts := range []string{"1718000002.000001", "1718000000.000001", "1718000001.000001"} {
		s.InsertMessage(Message{ChannelID: "C1", TS: ts, CaseID: c.ID, UserID: "U2",
			Text: "m" + ts, Raw: "{}", Source: SourceEvent})
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len = %d", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i-1].TS > msgs[i].TS {
			t.Errorf("not chronological: %s before %s", msgs[i-1].TS, msgs[i].TS)
		}
	}
}

func TestPMRunLifecycle(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "low", "public", "U1")

	runID, err := s.BeginPMRun(c.ID)
	if err != nil {
		t.Fatalf("BeginPMRun: %v", err)
	}
	// Second concurrent run refused.
	if _, err := s.BeginPMRun(c.ID); !errors.Is(err, ErrPMRunning) {
		t.Errorf("second BeginPMRun err = %v, want ErrPMRunning", err)
	}

	if err := s.FailPMRun(runID, "boom"); err != nil {
		t.Fatalf("FailPMRun: %v", err)
	}
	r, err := s.LatestPMRun(c.ID)
	if err != nil || r.Status != "failed" || r.Error != "boom" {
		t.Errorf("latest = %+v, err %v", r, err)
	}

	// After failure a new run may start.
	if _, err := s.BeginPMRun(c.ID); err != nil {
		t.Errorf("BeginPMRun after failure: %v", err)
	}
}

func TestFinalizePMRunReplacesKnowledge(t *testing.T) {
	fixed := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "t.db"),
		WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, _ := s.CreateCase("a", "low", "public", "U1")
	c2, _ := s.CreateCase("b", "low", "public", "U1")

	build := func(titles ...string) func(int, string) KnowledgeRow {
		return func(i int, tacticID string) KnowledgeRow {
			return KnowledgeRow{TacticID: tacticID, Title: titles[i], Category: "log-analysis",
				Confidence: "confirmed", TagsJSON: `["x"]`, Summary: "s",
				DocJSON: `{"id":"` + tacticID + `"}`, DocMD: "# " + titles[i]}
		}
	}

	// Another case already holds today's 001.
	run2, _ := s.BeginPMRun(c2.ID)
	if err := s.FinalizePMRun(run2, c2.ID, "{}", "# r", 1, build("other")); err != nil {
		t.Fatalf("finalize c2: %v", err)
	}

	run1, _ := s.BeginPMRun(c.ID)
	if err := s.FinalizePMRun(run1, c.ID, `{"a":1}`, "# report", 2, build("t1", "t2")); err != nil {
		t.Fatalf("finalize c1: %v", err)
	}

	rows, err := s.KnowledgeByCase(c.ID)
	if err != nil {
		t.Fatalf("KnowledgeByCase: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("knowledge rows = %d, want 2", len(rows))
	}
	// IDs continue from the day's max (other case took 001).
	if rows[0].TacticID != "tac-20260610-002" || rows[1].TacticID != "tac-20260610-003" {
		t.Errorf("tactic ids = %s, %s", rows[0].TacticID, rows[1].TacticID)
	}

	r, _ := s.LatestPMRun(c.ID)
	if r.Status != "done" || r.ReportMD != "# report" {
		t.Errorf("run = %+v", r)
	}

	// Re-run replaces this case's knowledge; the other case's stays.
	run3, _ := s.BeginPMRun(c.ID)
	if err := s.FinalizePMRun(run3, c.ID, "{}", "# v2", 1, build("t3")); err != nil {
		t.Fatalf("finalize rerun: %v", err)
	}
	rows, _ = s.KnowledgeByCase(c.ID)
	if len(rows) != 1 || rows[0].Title != "t3" {
		t.Errorf("after rerun rows = %+v", rows)
	}
	other, _ := s.KnowledgeByCase(c2.ID)
	if len(other) != 1 {
		t.Errorf("other case knowledge lost: %+v", other)
	}
}

func TestFailStaleRuns(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "low", "public", "U1")
	s.BeginPMRun(c.ID)

	n, err := s.FailStaleRuns()
	if err != nil {
		t.Fatalf("FailStaleRuns: %v", err)
	}
	if n != 1 {
		t.Errorf("stale = %d, want 1", n)
	}
	r, _ := s.LatestPMRun(c.ID)
	if r.Status != "failed" || r.Error != "interrupted by restart" {
		t.Errorf("run = %+v", r)
	}
}

func TestSchemaVersionIsThree(t *testing.T) {
	s := tempDB(t)
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != "3" {
		t.Errorf("SchemaVersion = %q, want 3", v)
	}
}

// seedKnowledge inserts a knowledge row via a finalized PM run so
// the cross-case readers have data to query.
func seedKnowledge(t *testing.T, s *Store, caseID int64, rows ...KnowledgeRow) {
	t.Helper()
	runID, err := s.BeginPMRun(caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizePMRun(runID, caseID, "{}", "# r", len(rows),
		func(i int, tacticID string) KnowledgeRow {
			r := rows[i]
			r.TacticID = tacticID
			return r
		}); err != nil {
		t.Fatal(err)
	}
}

func TestListAllKnowledge(t *testing.T) {
	s := tempDB(t)
	c1, _ := s.CreateCase("a", "high", "private", "U1")
	c2, _ := s.CreateCase("b", "low", "public", "U1")
	seedKnowledge(t, s, c1.ID, KnowledgeRow{Title: "disk check", Category: "log-analysis",
		Confidence: "confirmed", TagsJSON: `["disk"]`, Summary: "s1", DocJSON: "{}", DocMD: "# disk"})
	seedKnowledge(t, s, c2.ID, KnowledgeRow{Title: "auth review", Category: "authentication-analysis",
		Confidence: "inferred", TagsJSON: `["auth"]`, Summary: "s2", DocJSON: "{}", DocMD: "# auth"})

	docs, err := s.ListAllKnowledge()
	if err != nil {
		t.Fatalf("ListAllKnowledge: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	// Ordered by tactic_id, both have full fields.
	if docs[0].CaseID != c1.ID || docs[0].Title != "disk check" || docs[0].CreatedAt.IsZero() {
		t.Errorf("doc[0] = %+v", docs[0])
	}
	if docs[1].DocMD != "# auth" {
		t.Errorf("doc[1].DocMD = %q", docs[1].DocMD)
	}
}

func TestSearchKnowledge(t *testing.T) {
	s := tempDB(t)
	c, _ := s.CreateCase("a", "high", "private", "U1")
	seedKnowledge(t, s, c.ID,
		KnowledgeRow{Title: "Check systemd logs", Category: "linux-systemd",
			Confidence: "confirmed", TagsJSON: `["persistence","linux"]`, Summary: "service restarts", DocJSON: "{}", DocMD: "#"},
		KnowledgeRow{Title: "Review auth.log", Category: "authentication-analysis",
			Confidence: "inferred", TagsJSON: `["auth"]`, Summary: "failed logins", DocJSON: "{}", DocMD: "#"},
		KnowledgeRow{Title: "Inspect crontab", Category: "linux-systemd",
			Confidence: "confirmed", TagsJSON: `["persistence"]`, Summary: "scheduled tasks", DocJSON: "{}", DocMD: "#"},
	)

	// Empty filter returns all.
	all, _ := s.SearchKnowledge(nil, nil, "")
	if len(all) != 3 {
		t.Errorf("empty filter = %d, want 3", len(all))
	}

	// Term matches title.
	got, _ := s.SearchKnowledge([]string{"systemd"}, nil, "")
	if len(got) != 1 || got[0].Title != "Check systemd logs" {
		t.Errorf("term 'systemd' = %+v", got)
	}

	// Term matches summary OR tags (login in summary, auth in tags).
	got, _ = s.SearchKnowledge([]string{"logins"}, nil, "")
	if len(got) != 1 || got[0].Title != "Review auth.log" {
		t.Errorf("term 'logins' = %+v", got)
	}

	// Tag filter.
	got, _ = s.SearchKnowledge(nil, []string{"persistence"}, "")
	if len(got) != 2 {
		t.Errorf("tag 'persistence' = %d, want 2", len(got))
	}

	// Category AND-ed with terms.
	got, _ = s.SearchKnowledge([]string{"crontab", "auth"}, nil, "linux-systemd")
	if len(got) != 1 || got[0].Title != "Inspect crontab" {
		t.Errorf("category + terms = %+v", got)
	}

	// SQL-injection-y term is parameterized: matches nothing literally.
	got, _ = s.SearchKnowledge([]string{"%' OR '1'='1"}, nil, "")
	if len(got) != 0 {
		t.Errorf("injection term = %d, want 0 (parameterized)", len(got))
	}
}

func TestMigrateV2ToV3(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Minimal v2 fixture (meta says 2, knowledge table present).
	for _, stmt := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '2')`,
		`CREATE TABLE cases (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
			severity TEXT NOT NULL, visibility TEXT NOT NULL, channel_id TEXT UNIQUE,
			channel_name TEXT, state TEXT NOT NULL DEFAULT 'creating', opened_by TEXT NOT NULL,
			opened_at TEXT NOT NULL, closed_by TEXT, closed_at TEXT)`,
		`CREATE TABLE knowledge (id INTEGER PRIMARY KEY AUTOINCREMENT, case_id INTEGER NOT NULL,
			tactic_id TEXT NOT NULL UNIQUE, title TEXT NOT NULL, category TEXT NOT NULL,
			confidence TEXT NOT NULL, tags TEXT NOT NULL DEFAULT '[]', summary TEXT NOT NULL,
			doc_json TEXT NOT NULL, doc_md TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`INSERT INTO cases (title,severity,visibility,channel_id,channel_name,state,opened_by,opened_at)
			VALUES ('legacy','high','private','C1','ir-0001','open','U1','2026-06-01T00:00:00Z')`,
		`INSERT INTO knowledge (case_id,tactic_id,title,category,confidence,tags,summary,doc_json,doc_md,created_at)
			VALUES (1,'tac-20260601-001','t','log-analysis','confirmed','[]','s','{}','#','2026-06-01T00:00:00Z')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("v2 fixture: %v", err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open v2 db: %v", err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != "3" {
		t.Errorf("migrated version = %q, want 3", v)
	}
	// Existing knowledge survives and is queryable via the new reader.
	docs, err := s.ListAllKnowledge()
	if err != nil || len(docs) != 1 || docs[0].TacticID != "tac-20260601-001" {
		t.Errorf("docs = %+v, err %v", docs, err)
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
