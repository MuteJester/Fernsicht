package parse

import (
	"regexp"
	"strconv"
)

// fractionBracketParser matches `[N/M]` patterns. Common in:
//
//   Make output:        [1/2] Compiling foo.o
//   Pytest progress:    test_foo.py::test_one [ 1/45]
//   Custom scripts
//
// Single-shot matches (e.g., one-off Make line) are NOT enough for the
// confidence layer to lock onto this parser; the confidence threshold
// requires multiple matches in a window before fires count.
var fractionBracketRegex = regexp.MustCompile(`\[\s*(\d+)\s*/\s*(\d+)\s*\]`)

type fractionBracketParser struct{}

func (fractionBracketParser) Name() string { return "fraction-bracket" }

func (fractionBracketParser) Match(line string) (Tick, bool) {
	m := fractionBracketRegex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	return numTotal(m[1], m[2])
}

// fractionOfParser matches `N of M` (and a stricter form of `N/M`
// without brackets, requiring word boundaries to avoid colliding with
// random "/M" tokens in paths).
//
// Examples:
//   Processing 5 of 100
//   Imported 50 / 200 records
var fractionOfRegex = regexp.MustCompile(
	`\b(\d+)\s*(?:of|/)\s*(\d+)\b`,
)

type fractionOfParser struct{}

func (fractionOfParser) Name() string { return "fraction-of" }

func (fractionOfParser) Match(line string) (Tick, bool) {
	// To avoid false positives from "200/300 paths in /tmp/foo/bar"
	// (which would match "300 paths"), we require either "of" or
	// surrounding context that's progress-shaped. The regex already
	// handles "of"; for the "/" case, demand the line ALSO contain a
	// progress hint (action verb or a unit-y word).
	m := fractionOfRegex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	matched := m[0]
	if !containsAtAny(matched, "of") && !lineLooksLikeProgress(line) {
		return Tick{}, false
	}
	return numTotal(m[1], m[2])
}

// numTotal converts captured n / total strings into a Tick.
func numTotal(nStr, totalStr string) (Tick, bool) {
	n, err1 := strconv.Atoi(nStr)
	total, err2 := strconv.Atoi(totalStr)
	if err1 != nil || err2 != nil || total <= 0 {
		return Tick{}, false
	}
	return Tick{
		Value: computeValue(n, total),
		N:     n,
		Total: total,
	}, true
}

func containsAtAny(s string, sub string) bool {
	// Tiny convenience over strings.Contains so we don't import strings
	// in this file just for one call. (Also kept inline so the regex
	// match cost dominates.)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// lineLooksLikeProgress is a simple heuristic to distinguish progress
// lines like "Processing 5/100 files" from path-like lines like
// "writing /tmp/foo/bar". Returns true when the line contains an
// action verb or progress-suggestive vocabulary.
//
// Deliberately conservative — false positives cause spurious ticks,
// which the confidence layer then has to suppress.
func lineLooksLikeProgress(line string) bool {
	keywords := []string{
		"process", "complet", "done", "finish", "load", "import",
		"write", "save", "download", "upload", "fetch", "transfer",
		"build", "compile", "test", "run", "scan", "iterat",
	}
	low := toLower(line)
	for _, k := range keywords {
		if containsAtAny(low, k) {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
