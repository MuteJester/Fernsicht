package parse

import (
	"regexp"
	"strconv"
)

// stepKeywordParser matches lines like:
//
//   Step 5
//   Step 5 of 100
//   Step 5/100
//   Epoch 5
//   Epoch 5/10
//   iteration 50
//   iter 50/1000
//   batch 5/40
//
// Single match without a Total provides a counter but no value; the
// caller (or wrap) decides how to surface that.
//
// Case-insensitive — matches "Step", "STEP", "step" alike.
var stepKeywordRegex = regexp.MustCompile(
	`(?i)\b(?:step|epoch|iteration|iter|batch)\s+(\d+)(?:\s*(?:of|/)\s*(\d+))?`,
)

type stepKeywordParser struct{}

func (stepKeywordParser) Name() string { return "step-keyword" }

func (stepKeywordParser) Match(line string) (Tick, bool) {
	m := stepKeywordRegex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return Tick{}, false
	}
	t := Tick{N: n}
	if len(m) > 2 && m[2] != "" {
		total, err := strconv.Atoi(m[2])
		if err == nil && total > 0 {
			t.Total = total
			t.Value = computeValue(n, total)
		}
	}
	return t, true
}
