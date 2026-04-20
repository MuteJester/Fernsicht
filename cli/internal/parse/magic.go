package parse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MagicPrefix is the literal bytes (with trailing space) that mark a
// magic-prefix line. Any line starting with this is intercepted,
// parsed, and STRIPPED from the forwarded output (so it doesn't leak
// into downstream pipes / log files).
const MagicPrefix = "__fernsicht__ "

// MagicEvent classifies a magic-prefix line. Most are progress; some
// are lifecycle frames that change the active task.
type MagicEvent int

const (
	MagicProgress MagicEvent = iota
	MagicStart
	MagicEnd
	MagicLabel
	MagicURL // request CLI to re-print the URL
)

func (e MagicEvent) String() string {
	switch e {
	case MagicProgress:
		return "progress"
	case MagicStart:
		return "start"
	case MagicEnd:
		return "end"
	case MagicLabel:
		return "label"
	case MagicURL:
		return "url"
	}
	return "?"
}

// MagicParse classifies and parses a single line.
//
// Three return-shape outcomes:
//
//   1. Line doesn't start with MagicPrefix: returns (zero, ok=false,
//      err=nil). Caller forwards the line normally.
//
//   2. Line is magic and parses cleanly: returns (event, ok=true,
//      err=nil). Caller acts on it (strip from output, fire tick).
//
//   3. Line is magic but malformed: returns (zero, ok=true, err=...).
//      Caller still strips the line (don't leak the typo downstream)
//      AND warns the user (or exits if --strict-magic).
//
// Both JSON and compact forms are supported per CLI plan §5.2 and
// §20.1.
func MagicParse(line string) (mp MagicLine, ok bool, err error) {
	if !strings.HasPrefix(line, MagicPrefix) {
		return MagicLine{}, false, nil
	}
	rest := strings.TrimSpace(line[len(MagicPrefix):])
	if rest == "" {
		return MagicLine{}, true,
			errors.New("magic: empty payload after prefix")
	}

	// JSON form starts with `{`.
	if rest[0] == '{' {
		return parseMagicJSON(rest)
	}
	return parseMagicCompact(rest)
}

// MagicLine is the parsed magic-prefix payload.
type MagicLine struct {
	Event MagicEvent
	Tick  Tick // populated for Progress + Start (initial tick semantics)
	Label string
	TaskID string
}

// --- JSON form ------------------------------------------------------

type magicJSON struct {
	Value   *float64 `json:"value,omitempty"`
	N       *int     `json:"n,omitempty"`
	Total   *int     `json:"total,omitempty"`
	Label   string   `json:"label,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	TaskID  string   `json:"task_id,omitempty"`
	Event   string   `json:"event,omitempty"`
}

func parseMagicJSON(payload string) (MagicLine, bool, error) {
	var raw magicJSON
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return MagicLine{}, true, fmt.Errorf("magic: invalid JSON: %v", err)
	}

	mp := MagicLine{Label: raw.Label, TaskID: raw.TaskID}
	switch strings.ToLower(raw.Event) {
	case "", "progress":
		mp.Event = MagicProgress
	case "start":
		mp.Event = MagicStart
	case "end":
		mp.Event = MagicEnd
	case "label":
		mp.Event = MagicLabel
		if raw.Label == "" {
			return MagicLine{}, true,
				errors.New(`magic: "label" event requires "label" field`)
		}
	case "url":
		mp.Event = MagicURL
	default:
		return MagicLine{}, true,
			fmt.Errorf("magic: unknown event %q", raw.Event)
	}

	if mp.Event == MagicProgress || mp.Event == MagicStart {
		t, err := buildTickFromJSON(raw)
		if err != nil {
			return MagicLine{}, true, err
		}
		t.Source = "magic-json"
		t.Label = raw.Label
		t.Unit = raw.Unit
		t.TaskID = raw.TaskID
		mp.Tick = t
	}
	return mp, true, nil
}

func buildTickFromJSON(raw magicJSON) (Tick, error) {
	t := Tick{}
	if raw.N != nil {
		t.N = *raw.N
	}
	if raw.Total != nil {
		t.Total = *raw.Total
		if t.Total < 0 {
			return Tick{}, errors.New("magic: total must be >= 0")
		}
	}
	if raw.Value != nil {
		t.Value = *raw.Value
		if t.Value < 0 || t.Value > 1 {
			return Tick{}, fmt.Errorf("magic: value %v out of range [0,1]", t.Value)
		}
	} else if t.Total > 0 {
		t.Value = computeValue(t.N, t.Total)
	}
	return t, nil
}

// --- Compact form ---------------------------------------------------
//
// Grammar (per CLI plan §20.1):
//
//   progress N[/TOTAL] [UNIT]
//   progress NN%
//   start LABEL
//   end [TASK_ID]
//   label LABEL
//   url
//
// LABEL may be quoted ("..." or '...') or bare (single token).
//
// We hand-tokenize because the syntax is small and `flag` / `pflag`
// would be overkill (and wouldn't preserve quoted-string semantics).

func parseMagicCompact(payload string) (MagicLine, bool, error) {
	verb, rest := splitVerb(payload)
	switch verb {
	case "progress":
		return parseCompactProgress(rest)
	case "start":
		return parseCompactStart(rest)
	case "end":
		return parseCompactEnd(rest)
	case "label":
		return parseCompactLabel(rest)
	case "url":
		if strings.TrimSpace(rest) != "" {
			return MagicLine{}, true,
				errors.New(`magic: "url" takes no arguments`)
		}
		return MagicLine{Event: MagicURL}, true, nil
	default:
		return MagicLine{}, true,
			fmt.Errorf("magic: unknown verb %q (expected progress/start/end/label/url or {JSON})", verb)
	}
}

func splitVerb(s string) (verb, rest string) {
	for i, c := range s {
		if c == ' ' || c == '\t' {
			return s[:i], strings.TrimSpace(s[i:])
		}
	}
	return s, ""
}

func parseCompactProgress(rest string) (MagicLine, bool, error) {
	// rest: "N", "N/TOTAL", "N/TOTAL UNIT", or "NN%".
	if rest == "" {
		return MagicLine{}, true,
			errors.New(`magic: "progress" requires a value`)
	}

	parts := strings.Fields(rest)
	first := parts[0]
	t := Tick{Source: "magic-compact"}

	if strings.HasSuffix(first, "%") {
		pct, err := strconv.ParseFloat(strings.TrimSuffix(first, "%"), 64)
		if err != nil || pct < 0 || pct > 100 {
			return MagicLine{}, true,
				fmt.Errorf("magic: invalid percent %q", first)
		}
		t.Value = pct / 100.0
	} else if slash := strings.IndexByte(first, '/'); slash > 0 {
		n, err1 := strconv.Atoi(first[:slash])
		total, err2 := strconv.Atoi(first[slash+1:])
		if err1 != nil || err2 != nil || total <= 0 {
			return MagicLine{}, true,
				fmt.Errorf("magic: invalid N/TOTAL %q", first)
		}
		t.N = n
		t.Total = total
		t.Value = computeValue(n, total)
	} else {
		n, err := strconv.Atoi(first)
		if err != nil {
			return MagicLine{}, true,
				fmt.Errorf("magic: invalid count %q", first)
		}
		t.N = n
	}

	if len(parts) > 1 {
		t.Unit = parts[1]
	}

	return MagicLine{Event: MagicProgress, Tick: t}, true, nil
}

func parseCompactStart(rest string) (MagicLine, bool, error) {
	label, _, err := parseQuotedOrBare(rest)
	if err != nil {
		return MagicLine{}, true, fmt.Errorf("magic: start: %v", err)
	}
	return MagicLine{Event: MagicStart, Label: label}, true, nil
}

func parseCompactEnd(rest string) (MagicLine, bool, error) {
	rest = strings.TrimSpace(rest)
	mp := MagicLine{Event: MagicEnd}
	if rest != "" {
		mp.TaskID = rest
	}
	return mp, true, nil
}

func parseCompactLabel(rest string) (MagicLine, bool, error) {
	label, _, err := parseQuotedOrBare(rest)
	if err != nil {
		return MagicLine{}, true, fmt.Errorf("magic: label: %v", err)
	}
	if label == "" {
		return MagicLine{}, true,
			errors.New(`magic: "label" requires a value`)
	}
	return MagicLine{Event: MagicLabel, Label: label}, true, nil
}

// parseQuotedOrBare returns the first token in s. If it starts with
// `"` or `'`, returns the contents up to the matching close quote;
// otherwise, the run of non-whitespace.
//
// Returns (token, restAfterToken, error).
func parseQuotedOrBare(s string) (string, string, error) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", "", nil
	}
	if s[0] == '"' || s[0] == '\'' {
		quote := s[0]
		end := strings.IndexByte(s[1:], quote)
		if end < 0 {
			return "", "", fmt.Errorf("unclosed %c quote", quote)
		}
		return s[1 : 1+end], s[1+end+1:], nil
	}
	// Bare token: take everything (compact "label" / "start" use the
	// rest of the line as the value, multi-word allowed).
	return s, "", nil
}
