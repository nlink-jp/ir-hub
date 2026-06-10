package defang

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantTypes []string
	}{
		{
			name:      "url",
			in:        "see https://evil.com/path?x=1 now",
			want:      "see hxxps://evil[.]com/path?x=1 now",
			wantTypes: []string{"url"},
		},
		{
			name:      "url with port keeps path dots",
			in:        "http://bad.example.org:8080/a.b/c.php",
			want:      "hxxp://bad[.]example[.]org:8080/a.b/c.php",
			wantTypes: []string{"url"},
		},
		{
			name:      "file url scheme only",
			in:        "file:///Users/x/mal.app",
			want:      "fxxle:///Users/x/mal.app",
			wantTypes: []string{"url"},
		},
		{
			name:      "email before domain",
			in:        "contact admin@evil.com asap",
			want:      "contact admin[@]evil[.]com asap",
			wantTypes: []string{"email"},
		},
		{
			name:      "ip",
			in:        "beacon to 192.168.1.1 stopped",
			want:      "beacon to 192[.]168[.]1[.]1 stopped",
			wantTypes: []string{"ip"},
		},
		{
			name:      "invalid ip untouched",
			in:        "version 999.1.2.3 ok",
			want:      "version 999.1.2.3 ok",
			wantTypes: nil,
		},
		{
			name:      "version string untouched",
			in:        "upgraded to 1.2.3.4.5",
			want:      "upgraded to 1.2.3.4.5",
			wantTypes: nil,
		},
		{
			name:      "standalone domain",
			in:        "lookup evil.com failed",
			want:      "lookup evil[.]com failed",
			wantTypes: []string{"domain"},
		},
		{
			name:      "domain inside url not double-defanged",
			in:        "https://evil.com and evil.com again",
			want:      "hxxps://evil[.]com and evil[.]com again",
			wantTypes: []string{"url", "domain"},
		},
		{
			name:      "hash recorded not rewritten",
			in:        "sha256 d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2 found",
			want:      "sha256 d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2 found",
			wantTypes: []string{"hash"},
		},
		{
			name:      "mixed ordering",
			in:        "192.168.0.5 hit hxxp test https://a.evil.jp/x then mail bob@corp.example.com",
			want:      "192[.]168[.]0[.]5 hit hxxp test hxxps://a[.]evil[.]jp/x then mail bob[@]corp[.]example[.]com",
			wantTypes: []string{"ip", "url", "email"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, iocs := Text(tt.in)
			if got != tt.want {
				t.Errorf("Text(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
			var types []string
			for _, i := range iocs {
				types = append(types, i.Type)
			}
			if strings.Join(types, ",") != strings.Join(tt.wantTypes, ",") {
				t.Errorf("types = %v, want %v", types, tt.wantTypes)
			}
		})
	}
}

// TestRedefangLLMOutput simulates the post-LLM pass: a model that
// refanged an indicator gets neutralized again, while already
// defanged forms stay stable.
func TestRedefangLLMOutput(t *testing.T) {
	llmOut := `{"root_cause": "beacon to https://evil.com from 10.0.0.7", "note": "was hxxps://evil[.]com"}`
	got, _ := Text(llmOut)
	if strings.Contains(got, "https://evil.com") {
		t.Errorf("refanged URL survived: %s", got)
	}
	if !strings.Contains(got, "10[.]0[.]0[.]7") {
		t.Errorf("refanged IP survived: %s", got)
	}
	if !strings.Contains(got, "hxxps://evil[.]com") {
		t.Errorf("already-defanged form corrupted: %s", got)
	}
}

func TestTextIdempotent(t *testing.T) {
	in := "https://evil.com 192.168.1.1 admin@evil.com"
	once, _ := Text(in)
	twice, _ := Text(once)
	if once != twice {
		t.Errorf("not idempotent:\n once  %q\n twice %q", once, twice)
	}
}

func TestEmailHelper(t *testing.T) {
	if got := Email("no-at-sign"); got != "no-at-sign" {
		t.Errorf("Email without @ = %q", got)
	}
}
