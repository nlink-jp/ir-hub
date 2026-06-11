package command

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    Parsed
		wantErr string
	}{
		{name: "empty", text: "", want: Parsed{}},
		{name: "whitespace only", text: "   ", want: Parsed{}},
		{name: "close", text: "close", want: Parsed{Sub: "close"}},
		{name: "status", text: "status", want: Parsed{Sub: "status"}},
		{name: "pm", text: "pm", want: Parsed{Sub: "pm"}},
		{name: "export", text: "export", want: Parsed{Sub: "export"}},
		{name: "export with args", text: "export now", wantErr: "takes no arguments"},
		{name: "close with args", text: "close now", wantErr: "takes no arguments"},
		{name: "unknown sub", text: "destroy", wantErr: "unknown subcommand"},
		{
			name: "new minimal",
			text: "new DB outage",
			want: Parsed{Sub: "new", New: &NewArgs{Title: "DB outage", Severity: "medium"}},
		},
		{
			name: "new with severity",
			text: "new DB outage --severity high",
			want: Parsed{Sub: "new", New: &NewArgs{Title: "DB outage", Severity: "high"}},
		},
		{
			name: "flags before title",
			text: "new --severity critical --private DB outage",
			want: Parsed{Sub: "new", New: &NewArgs{Title: "DB outage", Severity: "critical", Visibility: VisibilityPrivate}},
		},
		{
			name: "flag interleaved with title",
			text: "new DB --public outage",
			want: Parsed{Sub: "new", New: &NewArgs{Title: "DB outage", Severity: "medium", Visibility: VisibilityPublic}},
		},
		{name: "new without title", text: "new --severity low", wantErr: "requires a title"},
		{name: "bad severity", text: "new t --severity urgent", wantErr: "invalid severity"},
		{name: "severity missing value", text: "new t --severity", wantErr: "requires a value"},
		{name: "visibility conflict", text: "new t --private --public", wantErr: "mutually exclusive"},
		{name: "visibility conflict reversed", text: "new t --public --private", wantErr: "mutually exclusive"},
		{name: "unknown flag", text: "new t --force", wantErr: "unknown flag"},
		{
			name: "japanese title",
			text: "new 不審メール対応 --severity high",
			want: Parsed{Sub: "new", New: &NewArgs{Title: "不審メール対応", Severity: "high"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.text)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse(%q) err = %v, want containing %q", tt.text, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.text, err)
			}
			if got.Sub != tt.want.Sub {
				t.Errorf("Sub = %q, want %q", got.Sub, tt.want.Sub)
			}
			if (got.New == nil) != (tt.want.New == nil) {
				t.Fatalf("New = %+v, want %+v", got.New, tt.want.New)
			}
			if got.New != nil && *got.New != *tt.want.New {
				t.Errorf("New = %+v, want %+v", *got.New, *tt.want.New)
			}
		})
	}
}
