package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --- ParseCommand: happy paths ------------------------------------------

func TestParseHello(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := got.(HelloCmd)
	if !ok {
		t.Fatalf("expected HelloCmd, got %T", got)
	}
	if c.SDK != "r" || c.SDKVersion != "0.1.0" || c.Protocol != 1 {
		t.Errorf("unexpected fields: %+v", c)
	}
	if c.Op() != "hello" {
		t.Errorf("Op() = %q", c.Op())
	}
}

func TestParseSession(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"session","base_url":"https://signal.fernsicht.space","join_secret":"sec","max_viewers":4,"session_token_ttl_sec":3600}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := got.(SessionCmd)
	if !ok {
		t.Fatalf("expected SessionCmd, got %T", got)
	}
	if c.BaseURL != "https://signal.fernsicht.space" || c.JoinSecret != "sec" ||
		c.MaxViewers != 4 || c.SessionTokenTTLSec != 3600 {
		t.Errorf("unexpected fields: %+v", c)
	}
}

func TestParseSessionMinimal(t *testing.T) {
	// Only base_url is required; the rest are optional with defaults
	// applied later by the dispatcher.
	got, err := ParseCommand([]byte(`{"op":"session","base_url":"https://x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(SessionCmd)
	if c.JoinSecret != "" || c.MaxViewers != 0 {
		t.Errorf("unexpected non-zero defaults: %+v", c)
	}
}

func TestParseStart(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"start","task_id":"t1","label":"Training"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(StartCmd)
	if c.TaskID != "t1" || c.Label != "Training" {
		t.Errorf("unexpected fields: %+v", c)
	}
}

func TestParseProgressMinimal(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"progress","task_id":"t1","value":0.5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(ProgressCmd)
	if c.TaskID != "t1" {
		t.Errorf("task_id: %q", c.TaskID)
	}
	if c.Value == nil || *c.Value != 0.5 {
		t.Errorf("value: %v", c.Value)
	}
	if c.N != nil || c.Total != nil || c.Rate != nil || c.Elapsed != nil || c.ETA != nil {
		t.Errorf("optionals should be nil: %+v", c)
	}
	if c.Unit != "" {
		t.Errorf("unit should be empty (default applied later): %q", c.Unit)
	}
}

func TestParseProgressFull(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"progress","task_id":"t1","value":0.42,"n":420,"total":1000,"rate":18.5,"elapsed":22.7,"eta":31.3,"unit":"ep"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(ProgressCmd)
	if c.Value == nil || *c.Value != 0.42 {
		t.Fatalf("value: %v", c.Value)
	}
	if c.N == nil || *c.N != 420 {
		t.Errorf("n: %v", c.N)
	}
	if c.Total == nil || *c.Total != 1000 {
		t.Errorf("total: %v", c.Total)
	}
	if c.Rate == nil || *c.Rate != 18.5 {
		t.Errorf("rate: %v", c.Rate)
	}
	if c.Elapsed == nil || *c.Elapsed != 22.7 {
		t.Errorf("elapsed: %v", c.Elapsed)
	}
	if c.ETA == nil || *c.ETA != 31.3 {
		t.Errorf("eta: %v", c.ETA)
	}
	if c.Unit != "ep" {
		t.Errorf("unit: %q", c.Unit)
	}
}

func TestParseProgressZeroValueAccepted(t *testing.T) {
	// Explicit value=0 is valid (start of progress) and must not be
	// confused with "value missing".
	got, err := ParseCommand([]byte(`{"op":"progress","task_id":"t1","value":0}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(ProgressCmd)
	if c.Value == nil || *c.Value != 0 {
		t.Errorf("explicit zero rejected: %v", c.Value)
	}
}

func TestParseEnd(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"end","task_id":"t1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(EndCmd)
	if c.TaskID != "t1" {
		t.Errorf("task_id: %q", c.TaskID)
	}
}

func TestParseClose(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"close"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got.(CloseCmd); !ok {
		t.Errorf("expected CloseCmd, got %T", got)
	}
}

func TestParsePingWithID(t *testing.T) {
	got, err := ParseCommand([]byte(`{"op":"ping","id":"abc"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(PingCmd)
	if c.ID != "abc" {
		t.Errorf("id: %q", c.ID)
	}
}

func TestParsePingWithoutID(t *testing.T) {
	// id is optional.
	got, err := ParseCommand([]byte(`{"op":"ping"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := got.(PingCmd)
	if c.ID != "" {
		t.Errorf("id should be empty: %q", c.ID)
	}
}

// --- ParseCommand: rejection paths --------------------------------------

func TestParseRejectsEmptyLine(t *testing.T) {
	if _, err := ParseCommand([]byte(``)); err == nil {
		t.Error("expected error for empty input")
	}
	if _, err := ParseCommand([]byte(`   `)); err == nil {
		t.Error("expected error for whitespace-only input")
	}
}

func TestParseTrimsCRLF(t *testing.T) {
	// Windows SDKs may send \r\n line endings.
	got, err := ParseCommand([]byte("{\"op\":\"close\"}\r\n"))
	if err != nil {
		t.Fatalf("expected CRLF to be tolerated: %v", err)
	}
	if _, ok := got.(CloseCmd); !ok {
		t.Errorf("expected CloseCmd, got %T", got)
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	cases := []string{
		`{`,
		`}`,
		`not json at all`,
		`{"op":"hello"`,
		`{op:hello}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseCommand([]byte(in)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseRejectsMissingOp(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"sdk":"r"}`)); err == nil {
		t.Error("expected error for missing op")
	}
	if _, err := ParseCommand([]byte(`{"op":""}`)); err == nil {
		t.Error("expected error for empty op")
	}
}

func TestParseRejectsUnknownOp(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"bogus"}`)); err == nil {
		t.Error("expected error for unknown op")
	}
}

func TestParseHelloRejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"op":"hello"}`,
		`{"op":"hello","sdk":"r"}`,
		`{"op":"hello","sdk":"r","sdk_version":"0.1.0"}`,
		`{"op":"hello","sdk":"","sdk_version":"0.1.0","protocol":1}`,
		`{"op":"hello","sdk":"r","sdk_version":"","protocol":1}`,
		`{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":0}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseCommand([]byte(in)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseSessionRejectsMissingURL(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"session"}`)); err == nil {
		t.Error("expected error for missing base_url")
	}
	if _, err := ParseCommand([]byte(`{"op":"session","base_url":""}`)); err == nil {
		t.Error("expected error for empty base_url")
	}
}

func TestParseSessionRejectsNegativeNumbers(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"session","base_url":"x","max_viewers":-1}`)); err == nil {
		t.Error("expected error for negative max_viewers")
	}
	if _, err := ParseCommand([]byte(`{"op":"session","base_url":"x","session_token_ttl_sec":-1}`)); err == nil {
		t.Error("expected error for negative ttl")
	}
}

func TestParseStartRejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"op":"start"}`,
		`{"op":"start","task_id":"t1"}`,
		`{"op":"start","label":"Training"}`,
		`{"op":"start","task_id":"","label":"Training"}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseCommand([]byte(in)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseProgressRejectsMissingValue(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"progress","task_id":"t1"}`)); err == nil {
		t.Error("expected error for missing value")
	}
}

func TestParseProgressRejectsMissingTaskID(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"progress","value":0.5}`)); err == nil {
		t.Error("expected error for missing task_id")
	}
}

func TestParseEndRejectsMissingTaskID(t *testing.T) {
	if _, err := ParseCommand([]byte(`{"op":"end"}`)); err == nil {
		t.Error("expected error for missing task_id")
	}
}

// --- Events: marshal + round-trip ---------------------------------------

func TestWriteEventLineAppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteEventLine(&buf, NewPong("abc")); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("missing trailing newline: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly one newline: %q", out)
	}
}

// roundTrip marshals e via WriteEventLine and unmarshals into a generic
// map for field assertions.
func roundTrip(t *testing.T, e any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteEventLine(&buf, e); err != nil {
		t.Fatalf("WriteEventLine: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("re-parse: %v\nraw: %s", err, buf.String())
	}
	return m
}

func TestEventHelloAck(t *testing.T) {
	m := roundTrip(t, NewHelloAck("0.1.0"))
	if m["event"] != "hello_ack" || m["bridge_version"] != "0.1.0" || m["protocol"].(float64) != 1 {
		t.Errorf("hello_ack fields wrong: %+v", m)
	}
}

func TestEventSessionReady(t *testing.T) {
	m := roundTrip(t, NewSessionReady("abc12345", "secret", "https://x", "2026-04-19T12:00:00Z", 43200, 8, 25))
	if m["event"] != "session_ready" {
		t.Errorf("event: %v", m["event"])
	}
	if m["room_id"] != "abc12345" || m["sender_secret"] != "secret" {
		t.Errorf("identifiers: %+v", m)
	}
	if m["max_viewers"].(float64) != 8 || m["poll_interval_hint"].(float64) != 25 {
		t.Errorf("ints: %+v", m)
	}
}

func TestEventViewerJoinedLeft(t *testing.T) {
	m := roundTrip(t, NewViewerJoined("vega"))
	if m["event"] != "viewer_joined" || m["name"] != "vega" {
		t.Errorf("joined: %+v", m)
	}
	m = roundTrip(t, NewViewerLeft("orion"))
	if m["event"] != "viewer_left" || m["name"] != "orion" {
		t.Errorf("left: %+v", m)
	}
}

func TestEventViewerCountWithEmptyNames(t *testing.T) {
	m := roundTrip(t, NewViewerCount(nil))
	if m["count"].(float64) != 0 {
		t.Errorf("count should be 0: %v", m["count"])
	}
	// nil should serialize as [], NOT null, so SDK can iterate without
	// nil-checking.
	names, ok := m["names"].([]any)
	if !ok {
		t.Fatalf("names should be JSON array, got %T (%v)", m["names"], m["names"])
	}
	if len(names) != 0 {
		t.Errorf("names should be empty: %+v", names)
	}
}

func TestEventViewerCountWithNames(t *testing.T) {
	m := roundTrip(t, NewViewerCount([]string{"vega", "orion"}))
	if m["count"].(float64) != 2 {
		t.Errorf("count: %v", m["count"])
	}
	names := m["names"].([]any)
	if len(names) != 2 || names[0] != "vega" || names[1] != "orion" {
		t.Errorf("names: %+v", names)
	}
}

func TestEventPong(t *testing.T) {
	m := roundTrip(t, NewPong("abc"))
	if m["event"] != "pong" || m["id"] != "abc" {
		t.Errorf("pong: %+v", m)
	}
}

func TestEventError(t *testing.T) {
	m := roundTrip(t, NewError(ErrCodeSessionFailed, "boom", true))
	if m["event"] != "error" || m["code"] != "SESSION_FAILED" ||
		m["message"] != "boom" || m["fatal"] != true {
		t.Errorf("error fields: %+v", m)
	}
}

func TestEventClosed(t *testing.T) {
	m := roundTrip(t, NewClosed(CloseReasonSDKClose))
	if m["event"] != "closed" || m["reason"] != "sdk_close" {
		t.Errorf("closed: %+v", m)
	}
}

// failingWriter always returns an error from Write — used to verify
// that WriteEventLine surfaces I/O errors.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteEventLineSurfacesIOError(t *testing.T) {
	if err := WriteEventLine(failingWriter{}, NewPong("x")); err == nil {
		t.Error("expected I/O error to bubble up")
	}
}

// --- OrderingValidator --------------------------------------------------

func TestValidatorInitialState(t *testing.T) {
	v := NewOrderingValidator()
	if v.State() != "initial" {
		t.Errorf("State = %q", v.State())
	}
}

func TestValidatorRequiresHelloFirst(t *testing.T) {
	v := NewOrderingValidator()
	cases := []Command{
		SessionCmd{BaseURL: "x"},
		StartCmd{TaskID: "t1", Label: "x"},
		ProgressCmd{TaskID: "t1"},
		EndCmd{TaskID: "t1"},
		CloseCmd{},
	}
	for _, cmd := range cases {
		t.Run(cmd.Op(), func(t *testing.T) {
			if err := v.Validate(cmd); err == nil {
				t.Errorf("%s before hello should fail", cmd.Op())
			}
		})
	}
}

func TestValidatorAllowsPingBeforeHello(t *testing.T) {
	v := NewOrderingValidator()
	if err := v.Validate(PingCmd{}); err != nil {
		t.Errorf("ping before hello should be allowed: %v", err)
	}
}

func TestValidatorRejectsDuplicateHello(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	if err := v.Validate(HelloCmd{}); err == nil {
		t.Error("duplicate hello should fail")
	}
}

func TestValidatorRequiresSessionBeforeTaskOps(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	cases := []Command{
		StartCmd{TaskID: "t1", Label: "x"},
		ProgressCmd{TaskID: "t1"},
		EndCmd{TaskID: "t1"},
	}
	for _, cmd := range cases {
		t.Run(cmd.Op(), func(t *testing.T) {
			if err := v.Validate(cmd); err == nil {
				t.Errorf("%s before session should fail", cmd.Op())
			}
		})
	}
}

func TestValidatorAllowsCloseBeforeSession(t *testing.T) {
	// Close any time after hello is OK — graceful exit even before
	// opening a session.
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	if err := v.Validate(CloseCmd{}); err != nil {
		t.Errorf("close after hello should be allowed: %v", err)
	}
}

func TestValidatorRejectsDuplicateSession(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	v.Mark(SessionCmd{BaseURL: "x"})
	if err := v.Validate(SessionCmd{BaseURL: "x"}); err == nil {
		t.Error("duplicate session should fail")
	}
}

func TestValidatorAllowsTaskOpsAfterSession(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	v.Mark(SessionCmd{BaseURL: "x"})
	cases := []Command{
		StartCmd{TaskID: "t1", Label: "x"},
		ProgressCmd{TaskID: "t1"},
		EndCmd{TaskID: "t1"},
		CloseCmd{},
	}
	for _, cmd := range cases {
		t.Run(cmd.Op(), func(t *testing.T) {
			if err := v.Validate(cmd); err != nil {
				t.Errorf("%s after session should be allowed: %v", cmd.Op(), err)
			}
		})
	}
}

func TestValidatorSilentDropsAfterClose(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(HelloCmd{})
	v.Mark(SessionCmd{BaseURL: "x"})
	v.Mark(CloseCmd{})

	// All non-ping commands should silent-drop.
	for _, cmd := range []Command{
		ProgressCmd{TaskID: "t1"},
		StartCmd{TaskID: "t2", Label: "x"},
		EndCmd{TaskID: "t1"},
		CloseCmd{}, // close again — also silent drop
		HelloCmd{},
		SessionCmd{BaseURL: "x"},
	} {
		err := v.Validate(cmd)
		if !errors.Is(err, ErrSilentDrop) {
			t.Errorf("%s after close: expected ErrSilentDrop, got %v", cmd.Op(), err)
		}
	}

	// Ping is still allowed even after close (it's diagnostic).
	if err := v.Validate(PingCmd{}); err != nil {
		t.Errorf("ping after close should still be allowed: %v", err)
	}
}

func TestValidatorStateString(t *testing.T) {
	v := NewOrderingValidator()
	if v.State() != "initial" {
		t.Errorf("State = %q", v.State())
	}
	v.Mark(HelloCmd{})
	if v.State() != "hello_done" {
		t.Errorf("State = %q", v.State())
	}
	v.Mark(SessionCmd{BaseURL: "x"})
	if v.State() != "hello_done+session_open" {
		t.Errorf("State = %q", v.State())
	}
	v.Mark(CloseCmd{})
	if v.State() != "hello_done+session_open+closing" {
		t.Errorf("State = %q", v.State())
	}
}

// MarkOnNonStateChangingCmd shouldn't move the state machine.
func TestValidatorMarkIgnoresOtherCommands(t *testing.T) {
	v := NewOrderingValidator()
	v.Mark(PingCmd{})
	v.Mark(StartCmd{TaskID: "t1", Label: "x"})
	v.Mark(ProgressCmd{TaskID: "t1"})
	v.Mark(EndCmd{TaskID: "t1"})
	if v.State() != "initial" {
		t.Errorf("non-state-changing marks should leave state alone, got %q", v.State())
	}
}
