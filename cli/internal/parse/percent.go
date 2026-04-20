package parse

import (
	"regexp"
	"strconv"
)

// barePercentParser matches `<digits>%` anywhere in the line.
//
// LOWEST priority because it's the noisiest detector — any log line
// mentioning "42%" matches. The confidence layer's job is to
// suppress this when it fires once-and-done on a stray line, and to
// recognize it as a genuine progress signal when it fires repeatedly.
var barePercentRegex = regexp.MustCompile(`\b(\d{1,3}(?:\.\d+)?)\s*%`)

type barePercentParser struct{}

func (barePercentParser) Name() string { return "bare-percent" }

func (barePercentParser) Match(line string) (Tick, bool) {
	m := barePercentRegex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	if err != nil || pct < 0 || pct > 100 {
		return Tick{}, false
	}
	return Tick{Value: pct / 100.0}, true
}
