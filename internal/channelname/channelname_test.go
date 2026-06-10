package channelname

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		seq    int64
		title  string
		want   string
	}{
		{name: "basic", prefix: "ir-", seq: 42, title: "DB outage", want: "ir-0042-db-outage"},
		{name: "punctuation collapsed", prefix: "ir-", seq: 1, title: "API: 500s!! (urgent)", want: "ir-0001-api-500s-urgent"},
		{name: "japanese only title drops slug", prefix: "ir-", seq: 7, title: "不審メール対応", want: "ir-0007"},
		{name: "mixed keeps ascii", prefix: "ir-", seq: 7, title: "不審メール DKIM fail", want: "ir-0007-dkim-fail"},
		{name: "seq beyond 4 digits", prefix: "ir-", seq: 12345, title: "x", want: "ir-12345-x"},
		{name: "leading trailing junk", prefix: "ir-", seq: 2, title: "--- spaced out ---", want: "ir-0002-spaced-out"},
		{name: "uppercase lowered", prefix: "inc-", seq: 3, title: "RANSOMWARE", want: "inc-0003-ransomware"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.prefix, tt.seq, tt.title)
			if got != tt.want {
				t.Errorf("Build(%q, %d, %q) = %q, want %q", tt.prefix, tt.seq, tt.title, got, tt.want)
			}
		})
	}
}

func TestBuildLengthLimit(t *testing.T) {
	long := strings.Repeat("incident response ", 10) // 180 chars
	got := Build("ir-", 1, long)
	if len(got) > 80 {
		t.Errorf("len = %d, want <= 80 (%q)", len(got), got)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("name ends with '-': %q", got)
	}
	if !strings.HasPrefix(got, "ir-0001-") {
		t.Errorf("prefix lost: %q", got)
	}
}

func TestBuildValidCharset(t *testing.T) {
	got := Build("ir-", 9, "日本語 と English 混在 #タグ")
	for _, r := range got {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Errorf("invalid rune %q in %q", r, got)
		}
	}
}
