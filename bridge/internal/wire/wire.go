// Package wire serializes Fernsicht's pipe-delimited DataChannel frames.
//
// The frame format is shared with the Python SDK (publishers/python).
// Both implementations MUST produce byte-identical output for the same
// inputs; the contract is enforced by the cross-implementation corpus
// at testdata/corpus.json (consumed by both wire_test.go here and
// publishers/python/tests/test_wire.py).
//
// Frames the bridge sends to viewers:
//
//	ID|<peer_id>
//	START|<task_id>|<label>
//	P|<task_id>|<value>|<elapsed>|<eta>|<n>|<total>|<rate>|<unit>
//	END|<task_id>
//	V|<name1>|<name2>|...
//	K
//
// HELLO|<name> is sent by viewers to the sender and is parsed (not
// serialized) elsewhere; see internal/peer.
//
// Validation note: these helpers do not validate input emptiness or
// non-pipe content (other than presence sanitization, which is part
// of the V| frame's documented behavior). The dispatcher rejects
// invalid SDK commands before they reach this layer.
package wire

import (
	"fmt"
	"strings"
)

// Identity returns the ID| frame for the sender's peer ID.
func Identity(peerID string) string {
	return "ID|" + peerID
}

// Start returns the START| frame for a new task.
func Start(taskID, label string) string {
	return "START|" + taskID + "|" + label
}

// End returns the END| frame.
func End(taskID string) string {
	return "END|" + taskID
}

// KeepAlive returns the keepalive frame "K".
func KeepAlive() string {
	return "K"
}

// ProgressOpts holds the optional fields of the P| frame. nil pointers
// serialize as the "-" sentinel. Unit defaults to "it" when empty.
type ProgressOpts struct {
	Elapsed *float64
	ETA     *float64
	N       *int
	Total   *int
	Rate    *float64
	Unit    string
}

// Progress returns the P| frame. value is clamped to [0, 1].
//
// Field formatting matches the Python SDK exactly:
//   - value:   %.4f
//   - elapsed: %.1f or "-"
//   - eta:     %.1f or "-"
//   - n:       %d   or "-"
//   - total:   %d   or "-"
//   - rate:    %.2f or "-"
//   - unit:    string (defaults to "it" if empty)
func Progress(taskID string, value float64, opts ProgressOpts) string {
	if value < 0 {
		value = 0
	} else if value > 1 {
		value = 1
	}
	unit := opts.Unit
	if unit == "" {
		unit = "it"
	}
	return strings.Join([]string{
		"P",
		taskID,
		fmt.Sprintf("%.4f", value),
		optFloat(opts.Elapsed, "%.1f"),
		optFloat(opts.ETA, "%.1f"),
		optInt(opts.N),
		optInt(opts.Total),
		optFloat(opts.Rate, "%.2f"),
		unit,
	}, "|")
}

// Presence returns the V| frame for the current viewer roster.
//
// Sanitization mirrors the Python SDK: each name has pipes stripped
// (no escape mechanism in the wire format), is trimmed of surrounding
// whitespace, and truncated to 32 characters (rune-aware so multi-byte
// characters aren't split). Empty results are dropped. An empty input
// (or one that fully sanitizes away) produces just "V".
func Presence(viewers []string) string {
	parts := []string{"V"}
	for _, v := range viewers {
		if v == "" {
			continue
		}
		cleaned := strings.TrimSpace(strings.ReplaceAll(v, "|", ""))
		runes := []rune(cleaned)
		if len(runes) > 32 {
			cleaned = string(runes[:32])
		}
		if cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	return strings.Join(parts, "|")
}

func optFloat(p *float64, format string) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf(format, *p)
}

func optInt(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *p)
}
