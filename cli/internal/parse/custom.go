package parse

import (
	"fmt"
	"regexp"
	"strconv"
)

// CustomPattern compiles into a Parser per the user's configuration
// (--pattern flag or .fernsicht.toml [[detection.patterns]]).
//
// Capture interpretation (1-indexed; 0 means "not specified"):
//
//   - ValueCapture > 0 → group N is the progress fraction, parsed as
//     float64. Values > 1.0 are interpreted as percentages and divided
//     by 100 (e.g., "42" → 0.42).
//   - NCapture > 0     → group N is the items-completed count.
//   - TotalCapture > 0 → group N is the total-items count. If both
//     N and Total captures are set, Value is computed as N/Total
//     (overrides ValueCapture if both are present).
type CustomPattern struct {
	Name         string
	Regex        string
	ValueCapture int
	NCapture     int
	TotalCapture int
}

// Validate checks the pattern is sensible BEFORE we register it,
// returning a descriptive error so the user sees the problem at
// startup rather than silently-no-match at runtime.
func (p *CustomPattern) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("custom pattern: name is required")
	}
	if p.Regex == "" {
		return fmt.Errorf("custom pattern %q: regex is required", p.Name)
	}
	if _, err := regexp.Compile(p.Regex); err != nil {
		return fmt.Errorf("custom pattern %q: invalid regex: %w", p.Name, err)
	}
	if p.ValueCapture == 0 && p.NCapture == 0 && p.TotalCapture == 0 {
		return fmt.Errorf("custom pattern %q: at least one of value_capture / n_capture / total_capture must be > 0", p.Name)
	}
	return nil
}

// Compile builds a Parser from a validated CustomPattern.
func (p *CustomPattern) Compile() (Parser, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(p.Regex)
	if err != nil {
		return nil, err
	}
	return &customParser{
		name:     p.Name,
		regex:    re,
		valueCap: p.ValueCapture,
		nCap:     p.NCapture,
		totalCap: p.TotalCapture,
	}, nil
}

type customParser struct {
	name     string
	regex    *regexp.Regexp
	valueCap int
	nCap     int
	totalCap int
}

func (p *customParser) Name() string { return "custom:" + p.name }

func (p *customParser) Match(line string) (Tick, bool) {
	m := p.regex.FindStringSubmatch(line)
	if m == nil {
		return Tick{}, false
	}
	t := Tick{}

	if p.nCap > 0 && p.nCap < len(m) {
		n, err := strconv.Atoi(m[p.nCap])
		if err != nil {
			return Tick{}, false
		}
		t.N = n
	}
	if p.totalCap > 0 && p.totalCap < len(m) {
		total, err := strconv.Atoi(m[p.totalCap])
		if err != nil || total <= 0 {
			return Tick{}, false
		}
		t.Total = total
	}

	// If ValueCapture is specified, use it directly. Otherwise compute
	// from N/Total when both are present.
	if p.valueCap > 0 && p.valueCap < len(m) {
		v, err := strconv.ParseFloat(m[p.valueCap], 64)
		if err != nil {
			return Tick{}, false
		}
		if v > 1.0 {
			v /= 100.0 // percent → fraction
		}
		if v < 0 || v > 1.0 {
			return Tick{}, false
		}
		t.Value = v
	} else if t.Total > 0 {
		t.Value = computeValue(t.N, t.Total)
	} else if t.N > 0 {
		// N-only: no fraction available. Don't fire — caller has
		// no useful progress value.
		return Tick{}, false
	}

	return t, true
}
