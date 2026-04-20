package errcatalog

import (
	"strings"
	"testing"
)

func TestLookup_FindsKnownCodes(t *testing.T) {
	for _, code := range []string{"E001", "E010", "E020", "E099"} {
		if _, ok := Lookup(code); !ok {
			t.Errorf("expected to find %s", code)
		}
	}
}

func TestLookup_CaseInsensitive(t *testing.T) {
	if _, ok := Lookup("e001"); !ok {
		t.Error("lowercase code should match")
	}
	if _, ok := Lookup(" E001 "); !ok {
		t.Error("trimmed whitespace should match")
	}
}

func TestLookup_RejectsUnknown(t *testing.T) {
	if _, ok := Lookup("E999"); ok {
		t.Error("E999 should NOT be in catalog")
	}
	if _, ok := Lookup("garbage"); ok {
		t.Error("garbage should NOT match")
	}
}

func TestAll_ReturnsSortedAndComplete(t *testing.T) {
	all := All()
	if len(all) < 10 {
		t.Errorf("catalog seems sparse; got %d entries", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Code <= all[i-1].Code {
			t.Errorf("All() not sorted: %q before %q",
				all[i-1].Code, all[i].Code)
		}
	}
}

func TestFormat_HasFourSections(t *testing.T) {
	e, _ := Lookup("E001")
	out := Format(e)
	for _, want := range []string{"E001", "cause:", "hint:", "docs:"} {
		if !strings.Contains(out, want) {
			t.Errorf("Format missing %q in:\n%s", want, out)
		}
	}
}

func TestEachEntry_HasNonEmptyFields(t *testing.T) {
	for _, e := range All() {
		if e.Summary == "" || e.Cause == "" || e.Hint == "" {
			t.Errorf("%s: incomplete entry: %+v", e.Code, e)
		}
		if e.Class == "" {
			t.Errorf("%s: missing Class", e.Code)
		}
	}
}
