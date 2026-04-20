package parse

import (
	"regexp"
	"strconv"
)

// tqdmParser matches tqdm-style progress bars (Python tqdm library +
// pip + many ML training scripts). Sample lines:
//
//   Training: 50%|█████     | 50/100 [00:42<00:42,  1.18it/s]
//   100%|██████████| 80/80 [00:00<00:00, 5.0Mit/s]
//   Training:   0%|          | 0/100 [00:00<?, ?it/s]
//
// We anchor on the `<digits>%|` portion to avoid colliding with
// fraction-bracket / bare-percent on innocent log lines that happen
// to mention "50%" or "[1/2]".
//
// Captures: percent (1), n (2), total (3), unit (4, optional).
var tqdmRegex = regexp.MustCompile(
	`(\d+)%\s*\|.*?\|\s*(\d+)\s*/\s*(\d+)(?:\s*\[\s*[\d:?]+\s*<\s*[\d:?]+\s*,\s*([^\]]*)\])?`,
)

type tqdmParser struct{}

func (tqdmParser) Name() string { return "tqdm" }

func (tqdmParser) Match(line string) (Tick, bool) {
	m := tqdmRegex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	n, err1 := strconv.Atoi(m[2])
	total, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || total <= 0 {
		return Tick{}, false
	}
	t := Tick{
		Value: computeValue(n, total),
		N:     n,
		Total: total,
	}
	if len(m) > 4 && m[4] != "" {
		t.Unit = extractUnit(m[4])
	}
	return t, true
}

// extractUnit pulls the trailing unit from tqdm's rate suffix, e.g.,
// " 1.18it/s" → "it". Best-effort; returns empty if it can't find
// something sensible.
var unitRegex = regexp.MustCompile(`(?:[\d.]+)\s*([A-Za-z]+)/s`)

func extractUnit(suffix string) string {
	m := unitRegex.FindStringSubmatch(suffix)
	if m == nil {
		return ""
	}
	return m[1]
}
