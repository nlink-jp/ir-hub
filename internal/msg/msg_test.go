package msg

import (
	"reflect"
	"strings"
	"testing"
)

// TestCatalogsComplete guards against a translation being forgotten
// when a new string is added: every field of both catalogs must be
// non-empty.
func TestCatalogsComplete(t *testing.T) {
	for name, cat := range map[string]Catalog{"EN": EN, "JA": JA} {
		v := reflect.ValueOf(cat)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).String() == "" {
				t.Errorf("%s.%s is empty", name, v.Type().Field(i).Name)
			}
		}
	}
}

// TestCatalogVerbParity ensures translations keep the same fmt
// verbs in the same order as the English source — a swapped or
// dropped verb corrupts output at runtime.
func TestCatalogVerbParity(t *testing.T) {
	en := reflect.ValueOf(EN)
	ja := reflect.ValueOf(JA)
	for i := 0; i < en.NumField(); i++ {
		name := en.Type().Field(i).Name
		if got, want := verbs(ja.Field(i).String()), verbs(en.Field(i).String()); got != want {
			t.Errorf("%s: JA verbs %q != EN verbs %q", name, got, want)
		}
	}
}

// verbs extracts the sequence of fmt verbs from a template.
func verbs(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) {
			continue
		}
		i++
		// skip flags/width (e.g. %04d)
		for i < len(s) && (s[i] == '0' || s[i] == '1' || s[i] == '2' || s[i] == '3' ||
			s[i] == '4' || s[i] == '5' || s[i] == '6' || s[i] == '7' || s[i] == '8' ||
			s[i] == '9' || s[i] == '.' || s[i] == '+' || s[i] == '-' || s[i] == '#') {
			i++
		}
		if i < len(s) {
			if s[i] == '%' {
				continue // literal %%
			}
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

func TestFor(t *testing.T) {
	if For("ja") != &JA {
		t.Error("For(ja) != JA")
	}
	if For("en") != &EN {
		t.Error("For(en) != EN")
	}
	if For("") != &EN {
		t.Error("For('') should default to EN")
	}
	if For("fr") != &EN {
		t.Error("For(unknown) should default to EN")
	}
}

func TestF(t *testing.T) {
	c := For("ja")
	got := c.F(c.KickoffHeader, int64(42), "情報漏えい")
	if !strings.Contains(got, "#0042") || !strings.Contains(got, "情報漏えい") {
		t.Errorf("F = %q", got)
	}
}
