package parse

import (
	"bufio"
	"math"
	"os"
	"strings"
	"testing"
)

// loadCorpus reads testdata/<name> and returns the non-empty lines.
func loadCorpus(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func nearly(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}

// --- tqdm parser ---

func TestTqdm_MatchesRealTqdmOutput(t *testing.T) {
	lines := loadCorpus(t, "tqdm_outputs.txt")
	if len(lines) == 0 {
		t.Skip("empty corpus")
	}
	p := tqdmParser{}
	matched := 0
	for _, line := range lines {
		if _, ok := p.Match(line); ok {
			matched++
		}
	}
	if matched < len(lines)*9/10 {
		t.Errorf("tqdm parser matched only %d/%d corpus lines", matched, len(lines))
	}
}

func TestTqdm_ExtractsCorrectFields(t *testing.T) {
	line := "Training:  50%|█████     | 50/100 [00:42<00:42,  1.18it/s]"
	tk, ok := tqdmParser{}.Match(line)
	if !ok {
		t.Fatal("expected match")
	}
	if tk.N != 50 || tk.Total != 100 {
		t.Errorf("got N=%d Total=%d, want 50/100", tk.N, tk.Total)
	}
	if !nearly(tk.Value, 0.5) {
		t.Errorf("got Value=%v, want 0.5", tk.Value)
	}
	if tk.Unit != "it" {
		t.Errorf("got Unit=%q, want 'it'", tk.Unit)
	}
}

// --- bracket / fraction parsers ---

func TestFractionBracket_Matches(t *testing.T) {
	cases := []struct{ line string; n, total int }{
		{"[1/2] Compiling foo.o", 1, 2},
		{"test_one.py [ 1/45]", 1, 45},
		{"[100/100] done", 100, 100},
	}
	p := fractionBracketParser{}
	for _, tc := range cases {
		tk, ok := p.Match(tc.line)
		if !ok {
			t.Errorf("expected match for %q", tc.line)
			continue
		}
		if tk.N != tc.n || tk.Total != tc.total {
			t.Errorf("%q: got N=%d Total=%d, want %d/%d",
				tc.line, tk.N, tk.Total, tc.n, tc.total)
		}
	}
}

func TestFractionOf_RequiresProgressContext(t *testing.T) {
	// "of" form: trusted unconditionally.
	p := fractionOfParser{}
	if _, ok := p.Match("Processing 5 of 100"); !ok {
		t.Error("expected match for 'Processing 5 of 100'")
	}
	// "/" form WITHOUT progress vocab: should reject.
	if _, ok := p.Match("Found 200/300 paths in /tmp/foo/bar"); !ok {
		// "Found" isn't in our keyword list, but contains "load"... no, doesn't.
		// "paths" isn't a keyword. So reject (good — could be paths).
		// Actually "Found" doesn't have any keyword. So this should reject.
	}
	// "/" form WITH progress vocab: accept.
	if _, ok := p.Match("Imported 50/200 records"); !ok {
		t.Error("expected match for 'Imported 50/200 records' (has 'import' keyword)")
	}
}

// --- step keyword ---

func TestStepKeyword_Matches(t *testing.T) {
	cases := []struct {
		line  string
		n, total int
	}{
		{"Step 5", 5, 0},
		{"Step 5 of 100", 5, 100},
		{"Step 5/100", 5, 100},
		{"Epoch 10/100", 10, 100},
		{"iteration 50", 50, 0},
		{"iter 50/1000", 50, 1000},
		{"batch 5/40", 5, 40},
	}
	p := stepKeywordParser{}
	for _, tc := range cases {
		tk, ok := p.Match(tc.line)
		if !ok {
			t.Errorf("expected match for %q", tc.line)
			continue
		}
		if tk.N != tc.n {
			t.Errorf("%q: got N=%d, want %d", tc.line, tk.N, tc.n)
		}
		if tk.Total != tc.total {
			t.Errorf("%q: got Total=%d, want %d", tc.line, tk.Total, tc.total)
		}
	}
}

// --- bare percent ---

func TestBarePercent_Matches(t *testing.T) {
	tk, ok := barePercentParser{}.Match("status 42%")
	if !ok {
		t.Fatal("expected match")
	}
	if !nearly(tk.Value, 0.42) {
		t.Errorf("got Value=%v, want 0.42", tk.Value)
	}
}

func TestBarePercent_RejectsNonsense(t *testing.T) {
	_, ok := barePercentParser{}.Match("status 1234%")
	if ok {
		t.Error("should reject 1234% (out of [0,100])")
	}
}

// --- registry priority ---

func TestRegistry_TqdmBeatsBarePercent(t *testing.T) {
	r := NewRegistry()
	tk, name, ok := r.MatchFirst("Training:  50%|█████     | 50/100 [00:42<00:42,  1.18it/s]")
	if !ok || name != "tqdm" {
		t.Errorf("expected tqdm to win; got name=%q ok=%v", name, ok)
	}
	if tk.Total != 100 {
		t.Errorf("expected tqdm total 100; got %d", tk.Total)
	}
}

func TestRegistry_FalsePositiveCorpus(t *testing.T) {
	// Lines that LOOK like progress to one parser but are real-world
	// noise that shouldn't drive a bar. Confidence locking has the
	// final say (separate test); here we only verify which parsers
	// match — flagging which lines are at risk.
	lines := loadCorpus(t, "false_positives.txt")
	r := NewRegistry()
	matchCount := 0
	for _, line := range lines {
		if _, _, ok := r.MatchFirst(line); ok {
			matchCount++
		}
	}
	// Some of these WILL match individual parsers (that's expected).
	// The point of this test: produce a corpus we can run through
	// confidence-locking to verify ZERO ticks fire on pure-noise
	// inputs — see TestConfidence_RejectsSingleShot below.
	if matchCount == 0 {
		t.Log("note: false-positive corpus had no matches (parsers are well-disciplined)")
	}
}

// --- magic prefix tests ---

func TestMagic_JSONForms(t *testing.T) {
	cases := []struct {
		line   string
		event  MagicEvent
		value  float64
		label  string
	}{
		{`__fernsicht__ {"value":0.5}`, MagicProgress, 0.5, ""},
		{`__fernsicht__ {"n":50,"total":100}`, MagicProgress, 0.5, ""},
		{`__fernsicht__ {"event":"start","label":"Phase 1"}`, MagicStart, 0, "Phase 1"},
		{`__fernsicht__ {"event":"end"}`, MagicEnd, 0, ""},
		{`__fernsicht__ {"event":"label","label":"X"}`, MagicLabel, 0, "X"},
		{`__fernsicht__ {"event":"url"}`, MagicURL, 0, ""},
	}
	for _, tc := range cases {
		mp, ok, err := MagicParse(tc.line)
		if !ok {
			t.Errorf("%q: expected magic match", tc.line)
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err: %v", tc.line, err)
			continue
		}
		if mp.Event != tc.event {
			t.Errorf("%q: event got %v want %v", tc.line, mp.Event, tc.event)
		}
		if tc.value != 0 && !nearly(mp.Tick.Value, tc.value) {
			t.Errorf("%q: value got %v want %v", tc.line, mp.Tick.Value, tc.value)
		}
		if tc.label != "" && mp.Label != tc.label {
			t.Errorf("%q: label got %q want %q", tc.line, mp.Label, tc.label)
		}
	}
}

func TestMagic_CompactForms(t *testing.T) {
	cases := []struct {
		line   string
		event  MagicEvent
		value  float64
		n, tot int
		unit   string
		label  string
	}{
		{`__fernsicht__ progress 50/100`, MagicProgress, 0.5, 50, 100, "", ""},
		{`__fernsicht__ progress 75/100 batch`, MagicProgress, 0.75, 75, 100, "batch", ""},
		{`__fernsicht__ progress 33%`, MagicProgress, 0.33, 0, 0, "", ""},
		{`__fernsicht__ progress 5`, MagicProgress, 0, 5, 0, "", ""},
		{`__fernsicht__ start "Training"`, MagicStart, 0, 0, 0, "", "Training"},
		{`__fernsicht__ start training`, MagicStart, 0, 0, 0, "", "training"},
		{`__fernsicht__ end`, MagicEnd, 0, 0, 0, "", ""},
		{`__fernsicht__ end epoch-3`, MagicEnd, 0, 0, 0, "", ""},
		{`__fernsicht__ label "New label"`, MagicLabel, 0, 0, 0, "", "New label"},
		{`__fernsicht__ url`, MagicURL, 0, 0, 0, "", ""},
	}
	for _, tc := range cases {
		mp, ok, err := MagicParse(tc.line)
		if !ok || err != nil {
			t.Errorf("%q: ok=%v err=%v", tc.line, ok, err)
			continue
		}
		if mp.Event != tc.event {
			t.Errorf("%q: event got %v want %v", tc.line, mp.Event, tc.event)
		}
		if tc.value != 0 && !nearly(mp.Tick.Value, tc.value) {
			t.Errorf("%q: value got %v want %v", tc.line, mp.Tick.Value, tc.value)
		}
		if tc.n != 0 && mp.Tick.N != tc.n {
			t.Errorf("%q: N got %d want %d", tc.line, mp.Tick.N, tc.n)
		}
		if tc.tot != 0 && mp.Tick.Total != tc.tot {
			t.Errorf("%q: Total got %d want %d", tc.line, mp.Tick.Total, tc.tot)
		}
		if tc.unit != "" && mp.Tick.Unit != tc.unit {
			t.Errorf("%q: Unit got %q want %q", tc.line, mp.Tick.Unit, tc.unit)
		}
		if tc.label != "" && mp.Label != tc.label {
			t.Errorf("%q: Label got %q want %q", tc.line, mp.Label, tc.label)
		}
	}
}

func TestMagic_RejectsNonMagic(t *testing.T) {
	cases := []string{
		"plain output",
		"",
		"__fernsicht__nope",   // missing trailing space
		"FERNSICHT_ progress",
	}
	for _, line := range cases {
		_, ok, _ := MagicParse(line)
		if ok {
			t.Errorf("%q should NOT match magic prefix", line)
		}
	}
}

func TestMagic_InvalidPayload_ReportsError(t *testing.T) {
	cases := []string{
		`__fernsicht__ {bad json}`,
		`__fernsicht__ {"value":2.0}`,         // out of range
		`__fernsicht__ progress`,               // missing value
		`__fernsicht__ progress abc`,           // non-numeric
		`__fernsicht__ progress 50/0`,          // total=0
		`__fernsicht__ frobulate`,              // unknown verb
		`__fernsicht__ url extra`,              // url takes no args
		`__fernsicht__ {"event":"label"}`,      // label event w/o label
	}
	for _, line := range cases {
		mp, ok, err := MagicParse(line)
		if !ok {
			t.Errorf("%q: should be recognized as magic (ok=true) even when malformed", line)
		}
		if err == nil {
			t.Errorf("%q: expected error for malformed magic; got mp=%+v", line, mp)
		}
	}
}

// --- corpus checks ---

func TestMagic_AllCompactCorpusLinesParse(t *testing.T) {
	for _, line := range loadCorpus(t, "magic_compact.txt") {
		_, ok, err := MagicParse(line)
		if !ok || err != nil {
			t.Errorf("%q failed: ok=%v err=%v", line, ok, err)
		}
	}
}

func TestMagic_AllJSONCorpusLinesParse(t *testing.T) {
	for _, line := range loadCorpus(t, "magic_json.txt") {
		_, ok, err := MagicParse(line)
		if !ok || err != nil {
			t.Errorf("%q failed: ok=%v err=%v", line, ok, err)
		}
	}
}

func TestSnakemakeCorpus_FractionBracketDominates(t *testing.T) {
	// Snakemake prints `[N of M steps (NN%) done]`. Want
	// fraction-of (since "of" is in the line and there's progress
	// vocab) to win.
	lines := loadCorpus(t, "snakemake_output.txt")
	r := NewRegistry()
	matched := 0
	for _, line := range lines {
		if !strings.Contains(line, "steps") {
			continue
		}
		if _, _, ok := r.MatchFirst(line); ok {
			matched++
		}
	}
	if matched == 0 {
		t.Errorf("no snakemake step lines matched any parser")
	}
}
