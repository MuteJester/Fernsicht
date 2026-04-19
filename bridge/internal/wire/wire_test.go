package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// vector is one entry in testdata/corpus.json. The corpus is the
// cross-implementation contract — the same file is loaded by
// publishers/python/tests/test_wire.py and asserts byte-equality
// against the Python serializers.
type vector struct {
	Name     string                 `json:"name"`
	Fn       string                 `json:"fn"`
	Args     map[string]interface{} `json:"args"`
	Expected string                 `json:"expected"`
}

type corpus struct {
	Version int      `json:"version"`
	Vectors []vector `json:"vectors"`
}

func loadCorpus(t *testing.T) corpus {
	t.Helper()
	data, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var c corpus
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(c.Vectors) == 0 {
		t.Fatal("corpus contains no vectors")
	}
	return c
}

// TestCorpus runs every vector in the shared corpus through the local
// serializers and asserts byte-equality. This is the contract test
// shared with the Python SDK; if a vector fails here it likely also
// fails in publishers/python/tests/test_wire.py.
func TestCorpus(t *testing.T) {
	c := loadCorpus(t)
	for _, v := range c.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			got, err := dispatch(v)
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if got != v.Expected {
				t.Errorf("\n want: %q\n  got: %q", v.Expected, got)
			}
		})
	}
}

// dispatch maps a corpus vector to its serializer call. The arg
// extraction tolerates JSON's number type (float64 only) and converts
// to int where the Python signature expects an int.
func dispatch(v vector) (string, error) {
	switch v.Fn {
	case "identity":
		return Identity(asString(v.Args, "peer_id")), nil
	case "start":
		return Start(asString(v.Args, "task_id"), asString(v.Args, "label")), nil
	case "end":
		return End(asString(v.Args, "task_id")), nil
	case "keepalive":
		return KeepAlive(), nil
	case "progress":
		return Progress(
			asString(v.Args, "task_id"),
			asFloat(v.Args, "value"),
			ProgressOpts{
				Elapsed: asOptFloat(v.Args, "elapsed"),
				ETA:     asOptFloat(v.Args, "eta"),
				N:       asOptInt(v.Args, "n"),
				Total:   asOptInt(v.Args, "total"),
				Rate:    asOptFloat(v.Args, "rate"),
				Unit:    asOptString(v.Args, "unit"),
			},
		), nil
	case "presence":
		return Presence(asStringSlice(v.Args, "viewers")), nil
	}
	return "", fmt.Errorf("unknown fn: %q", v.Fn)
}

// --- Argument extractors -------------------------------------------------
// JSON unmarshals all numbers as float64. We coerce to int where the
// vector's named arg is documented as an integer.

func asString(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func asOptString(args map[string]interface{}, key string) string {
	return asString(args, key)
}

func asFloat(args map[string]interface{}, key string) float64 {
	v, ok := args[key]
	if !ok {
		return 0
	}
	f, _ := v.(float64)
	return f
}

func asOptFloat(args map[string]interface{}, key string) *float64 {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

func asOptInt(args map[string]interface{}, key string) *int {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

func asStringSlice(args map[string]interface{}, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

// --- Direct unit tests ---------------------------------------------------
// These cover behaviors that are simpler to assert directly than via
// the corpus (especially error paths and bit-level details).

func TestIdentity(t *testing.T) {
	if got := Identity("p1"); got != "ID|p1" {
		t.Errorf("got %q", got)
	}
}

func TestStart(t *testing.T) {
	if got := Start("t1", "label"); got != "START|t1|label" {
		t.Errorf("got %q", got)
	}
}

func TestEnd(t *testing.T) {
	if got := End("t1"); got != "END|t1" {
		t.Errorf("got %q", got)
	}
}

func TestKeepAlive(t *testing.T) {
	if got := KeepAlive(); got != "K" {
		t.Errorf("got %q", got)
	}
}

func TestProgressNineFields(t *testing.T) {
	// The frontend parser expects exactly 9 pipe-delimited fields.
	got := Progress("t1", 0.5, ProgressOpts{})
	parts := splitPipes(got)
	if len(parts) != 9 {
		t.Errorf("expected 9 fields, got %d: %q", len(parts), got)
	}
}

func TestProgressClampsValueAboveOne(t *testing.T) {
	got := Progress("t1", 1.5, ProgressOpts{})
	parts := splitPipes(got)
	if parts[2] != "1.0000" {
		t.Errorf("value not clamped above 1: %q", parts[2])
	}
}

func TestProgressClampsNegativeValue(t *testing.T) {
	got := Progress("t1", -0.5, ProgressOpts{})
	parts := splitPipes(got)
	if parts[2] != "0.0000" {
		t.Errorf("value not clamped below 0: %q", parts[2])
	}
}

func TestProgressDefaultUnit(t *testing.T) {
	got := Progress("t1", 0.5, ProgressOpts{})
	parts := splitPipes(got)
	if parts[8] != "it" {
		t.Errorf("unit default not 'it': %q", parts[8])
	}
}

func TestPresenceEmpty(t *testing.T) {
	if got := Presence(nil); got != "V" {
		t.Errorf("nil → %q", got)
	}
	if got := Presence([]string{}); got != "V" {
		t.Errorf("empty slice → %q", got)
	}
	if got := Presence([]string{"", "  ", ""}); got != "V" {
		t.Errorf("all-empty after sanitize → %q", got)
	}
}

func TestPresenceTruncatesAt32Runes(t *testing.T) {
	long := ""
	for i := 0; i < 64; i++ {
		long += "a"
	}
	got := Presence([]string{long})
	parts := splitPipes(got)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if len([]rune(parts[1])) != 32 {
		t.Errorf("expected 32-char name, got %d: %q", len([]rune(parts[1])), parts[1])
	}
}

func TestPresenceRuneAwareTruncation(t *testing.T) {
	// 32 multi-byte runes — must not be cut mid-rune.
	name := ""
	for i := 0; i < 40; i++ {
		name += "ñ"
	}
	got := Presence([]string{name})
	parts := splitPipes(got)
	if len([]rune(parts[1])) != 32 {
		t.Errorf("rune-aware truncation broken: got %d runes", len([]rune(parts[1])))
	}
}

// splitPipes splits a frame on '|' returning all parts (including
// empty trailing parts).
func splitPipes(s string) []string {
	parts := []string{}
	cur := ""
	for _, ch := range s {
		if ch == '|' {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(ch)
	}
	parts = append(parts, cur)
	return parts
}
