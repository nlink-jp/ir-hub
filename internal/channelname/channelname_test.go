package channelname

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
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
		{name: "japanese title kept", prefix: "ir-", seq: 7, title: "不審メール対応", want: "ir-0007-不審メール対応"},
		{name: "japanese with punctuation", prefix: "ir-", seq: 8, title: "サービスからの情報漏えい(本番)", want: "ir-0008-サービスからの情報漏えい-本番"},
		{name: "mixed keeps both", prefix: "ir-", seq: 7, title: "不審メール DKIM fail", want: "ir-0007-不審メール-dkim-fail"},
		{name: "symbols only drops slug", prefix: "ir-", seq: 9, title: "!!! --- ???", want: "ir-0009"},
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
	for name, long := range map[string]string{
		"ascii":    strings.Repeat("incident response ", 10),
		"japanese": strings.Repeat("情報漏えいインシデント", 10),
	} {
		t.Run(name, func(t *testing.T) {
			got := Build("ir-", 1, long)
			if n := utf8.RuneCountInString(got); n > 80 {
				t.Errorf("rune count = %d, want <= 80 (%q)", n, got)
			}
			if strings.HasSuffix(got, "-") {
				t.Errorf("name ends with '-': %q", got)
			}
			if !strings.HasPrefix(got, "ir-0001-") {
				t.Errorf("prefix lost: %q", got)
			}
		})
	}
}

func TestBuildValidCharset(t *testing.T) {
	got := Build("ir-", 9, "日本語 と English 混在 #タグ。終わり")
	for _, r := range got {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
			t.Errorf("invalid rune %q in %q", r, got)
		}
		if unicode.IsUpper(r) {
			t.Errorf("uppercase rune %q in %q", r, got)
		}
	}
	if !strings.Contains(got, "日本語") || !strings.Contains(got, "english") {
		t.Errorf("expected mixed content kept: %q", got)
	}
}
