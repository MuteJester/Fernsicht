// Package termui renders the small terminal-side adornments around
// the viewer URL: QR codes, OSC 8 hyperlinks, the first-run tip.
package termui

import (
	"fmt"
	"io"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// QR renders a QR code for url to w using Unicode block characters.
//
// Two block characters per cell pair (top + bottom of one terminal
// row), so the QR is half as tall as it is wide on most fonts —
// matching typical terminal aspect ratios.
//
// Uses the LOW recovery level (~7% redundancy). The viewer URL is
// short and there's no realistic damage source (it's printed to a
// terminal, not photographed off a scratched poster); LOW gives the
// smallest grid and therefore the smallest visual footprint.
func QR(w io.Writer, url string) error {
	q, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return fmt.Errorf("termui: qr encode %q: %w", url, err)
	}
	bitmap := q.Bitmap() // [][]bool — true = dark module
	if len(bitmap) == 0 {
		return nil
	}

	// We render two grid rows per terminal line using:
	//   top dark + bot dark   →  full block (█ = U+2588)
	//   top dark + bot light  →  upper half (▀ = U+2580)
	//   top light + bot dark  →  lower half (▄ = U+2584)
	//   top light + bot light →  space
	const (
		full  = "█"
		upper = "▀"
		lower = "▄"
		empty = " "
	)

	// One blank cell of left margin so the QR isn't flush against
	// the prompt edge.
	const leftPad = "  "

	var sb strings.Builder
	rows := len(bitmap)
	for y := 0; y < rows; y += 2 {
		sb.WriteString(leftPad)
		for x := 0; x < len(bitmap[y]); x++ {
			top := bitmap[y][x]
			bot := false
			if y+1 < rows {
				bot = bitmap[y+1][x]
			}
			switch {
			case top && bot:
				sb.WriteString(full)
			case top && !bot:
				sb.WriteString(upper)
			case !top && bot:
				sb.WriteString(lower)
			default:
				sb.WriteString(empty)
			}
		}
		sb.WriteByte('\n')
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// QREnabled is the policy decision: do we render a QR for url given
// these flags + environment? Centralized so the run subcommand
// doesn't have to repeat the heuristic.
//
// Defaults to "yes when stdout is a tty and the user hasn't opted
// out." `--no-qr` always wins; `--qr` always wins.
func QREnabled(forceOn, forceOff, stdoutIsTTY bool) bool {
	if forceOff {
		return false
	}
	if forceOn {
		return true
	}
	return stdoutIsTTY
}
