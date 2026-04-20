package parse

import (
	"strings"
	"testing"
)

func TestCustomPattern_Validate(t *testing.T) {
	cases := []struct {
		name string
		p    CustomPattern
		ok   bool
	}{
		{"missing-name", CustomPattern{Regex: "x", ValueCapture: 1}, false},
		{"missing-regex", CustomPattern{Name: "x", ValueCapture: 1}, false},
		{"bad-regex", CustomPattern{Name: "x", Regex: "([invalid", ValueCapture: 1}, false},
		{"no-captures", CustomPattern{Name: "x", Regex: "x"}, false},
		{"value-only", CustomPattern{Name: "x", Regex: "x", ValueCapture: 1}, true},
		{"n-and-total", CustomPattern{Name: "x", Regex: "x", NCapture: 1, TotalCapture: 2}, true},
	}
	for _, tc := range cases {
		err := tc.p.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: ok=%v err=%v", tc.name, tc.ok, err)
		}
	}
}

func TestCustomPattern_ValueCaptureAsPercent(t *testing.T) {
	p := CustomPattern{
		Name:         "eta",
		Regex:        `\[ETA: \d+:\d+\] (\d+)% complete`,
		ValueCapture: 1,
	}
	parser, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	tk, ok := parser.Match("[ETA: 5:30] 42% complete")
	if !ok {
		t.Fatal("expected match")
	}
	if !nearly(tk.Value, 0.42) {
		t.Errorf("Value got %v want 0.42", tk.Value)
	}
}

func TestCustomPattern_ValueCaptureAsFraction(t *testing.T) {
	p := CustomPattern{
		Name:         "frac",
		Regex:        `progress: ([0-9.]+)`,
		ValueCapture: 1,
	}
	parser, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := parser.Match("progress: 0.75")
	if !nearly(tk.Value, 0.75) {
		t.Errorf("got %v want 0.75", tk.Value)
	}
}

func TestCustomPattern_NTotalCapture(t *testing.T) {
	p := CustomPattern{
		Name:         "thingfile",
		Regex:        `Wrote (\d+) of (\d+)`,
		NCapture:     1,
		TotalCapture: 2,
	}
	parser, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	tk, ok := parser.Match("Wrote 50 of 200")
	if !ok {
		t.Fatal("expected match")
	}
	if tk.N != 50 || tk.Total != 200 {
		t.Errorf("N/Total got %d/%d want 50/200", tk.N, tk.Total)
	}
	if !nearly(tk.Value, 0.25) {
		t.Errorf("Value got %v want 0.25", tk.Value)
	}
}

func TestCustomPattern_NameIncludedInParserName(t *testing.T) {
	p := CustomPattern{Name: "myorch", Regex: "x", ValueCapture: 0, TotalCapture: 1}
	parser, _ := p.Compile()
	if !strings.HasPrefix(parser.Name(), "custom:") {
		t.Errorf("expected 'custom:' prefix; got %q", parser.Name())
	}
	if !strings.Contains(parser.Name(), "myorch") {
		t.Errorf("expected name to contain pattern name; got %q", parser.Name())
	}
}

func TestCustomPattern_RejectsBadValue(t *testing.T) {
	p := CustomPattern{Name: "x", Regex: `(\d+)`, ValueCapture: 1}
	parser, _ := p.Compile()
	// "200" → 2.0 → outside [0,1] → reject.
	if _, ok := parser.Match("status 200"); ok {
		t.Error("expected reject for value > 100%")
	}
}

func TestRegistry_AddCustom_AppendsAfterBuiltins(t *testing.T) {
	p := CustomPattern{Name: "x", Regex: `XX (\d+)`, ValueCapture: 1}
	parser, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.AddCustom(parser)
	all := r.All()
	if all[len(all)-1].Name() != "custom:x" {
		t.Errorf("custom should be last; got %q", all[len(all)-1].Name())
	}
}
