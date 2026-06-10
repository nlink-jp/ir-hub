package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var createdAt = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func sample() Tactic {
	return Tactic{
		Title:        "Check systemd logs for failed service restarts",
		Purpose:      "Identify service startup failures. Useful for crash loops.",
		Category:     "linux-systemd",
		Tools:        []string{"journalctl", "systemctl"},
		Procedure:    "1. Run journalctl -u svc -n 100\n2. Look for errors",
		Observations: "systemd logs show why services failed to start",
		Tags:         []string{"troubleshooting", "service-management"},
		Confidence:   "confirmed",
		Evidence:     "Command output was shared in the channel",
	}
}

func TestBuildJSON(t *testing.T) {
	doc, err := Build(sample(), "tac-20260610-001", "#ir-0003-incident", []string{"U1", "U2"}, createdAt)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(doc.JSON), &decoded); err != nil {
		t.Fatalf("doc JSON invalid: %v", err)
	}
	for k, want := range map[string]string{
		"id":         "tac-20260610-001",
		"confidence": "confirmed",
		"category":   "linux-systemd",
		"created_at": "2026-06-10",
	} {
		if decoded[k] != want {
			t.Errorf("JSON %s = %v, want %s", k, decoded[k], want)
		}
	}
	src := decoded["source"].(map[string]any)
	if src["channel"] != "#ir-0003-incident" {
		t.Errorf("source.channel = %v", src["channel"])
	}
}

func TestBuildMarkdown(t *testing.T) {
	doc, err := Build(sample(), "tac-20260610-001", "#ch", []string{"U1"}, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	md := doc.Markdown
	for _, want := range []string{
		"# Check systemd logs for failed service restarts",
		"`tac-20260610-001`",
		"`linux-systemd`",
		"## Purpose",
		"- `journalctl`",
		"## Procedure",
		"## Observations",
		"## Evidence",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBuildSummary(t *testing.T) {
	doc, _ := Build(sample(), "tac-20260610-001", "#ch", nil, createdAt)
	if doc.Summary != "Identify service startup failures." {
		t.Errorf("Summary = %q", doc.Summary)
	}

	jp := sample()
	jp.Purpose = "サービス起動失敗を特定する。クラッシュループに有効。"
	doc, _ = Build(jp, "tac-20260610-002", "#ch", nil, createdAt)
	if doc.Summary != "サービス起動失敗を特定する。" {
		t.Errorf("JA Summary = %q", doc.Summary)
	}
}

func TestBuildNilSlicesBecomeEmpty(t *testing.T) {
	tac := sample()
	tac.Tools, tac.Tags = nil, nil
	doc, err := Build(tac, "tac-20260610-001", "#ch", nil, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.JSON, "null") {
		t.Errorf("JSON contains null:\n%s", doc.JSON)
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Check systemd logs for failed service restarts": "check-systemd-logs-for-failed",
		"DNS exfil via TXT records!!":                    "dns-exfil-via-txt-records",
		"日本語タイトル":                                        "",
		"Mixed 日本語 and ASCII":                            "mixed-and-ascii",
	}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slug(strings.Repeat("abc ", 20)); len(got) > 30 || strings.HasSuffix(got, "-") {
		t.Errorf("Slug long = %q", got)
	}
}
