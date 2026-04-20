package main

import (
	"fmt"
	"io"
	"os"
)

// magicCommand prints the magic-prefix protocol reference.
//
// Mirrors plan §20.1 verbatim so users running `fernsicht magic`
// see the same canonical syntax as the website docs. We keep this
// in the binary (rather than fetching from the web) so the
// reference works offline.
func magicCommand(_ []string) int {
	printMagicReference(os.Stdout)
	return 0
}

const magicReferenceText = `
Magic-prefix protocol — explicit progress markers from any program.

Lines on the wrapped command's stdout that begin with the literal
prefix __fernsicht__ (with one trailing space) are intercepted by
the CLI: parsed for progress / lifecycle, then STRIPPED from the
forwarded output so they don't leak into log files / pipes.

The prefix can be disabled per-run with --no-magic, or exit-on-typo
with --strict-magic.

PREFIX
    __fernsicht__

JSON FORM
    __fernsicht__ {"value":0.5}
    __fernsicht__ {"n":50,"total":100}
    __fernsicht__ {"value":0.5,"n":50,"total":100,"label":"Training","unit":"batch"}
    __fernsicht__ {"event":"start","label":"Phase 1"}
    __fernsicht__ {"event":"end"}
    __fernsicht__ {"event":"end","task_id":"epoch-3"}
    __fernsicht__ {"event":"label","label":"New label"}
    __fernsicht__ {"event":"url"}

COMPACT FORM
    __fernsicht__ progress N[/TOTAL] [UNIT]
    __fernsicht__ progress NN%
    __fernsicht__ start "label with spaces"
    __fernsicht__ start barelabel
    __fernsicht__ end
    __fernsicht__ end TASK_ID
    __fernsicht__ label "New label"
    __fernsicht__ url

EXAMPLES (bash)
    for i in $(seq 1 100); do
        echo "__fernsicht__ progress $i/100"
        sleep 0.5
    done

EXAMPLES (python)
    import time
    for i in range(100):
        print(f"__fernsicht__ progress {i+1}/100", flush=True)
        time.sleep(0.5)

EXAMPLES (perl)
    for $i (1..100) {
        print "__fernsicht__ progress $i/100\n";
        sleep 0.5;
    }

CUSTOM CAPTURE FIELDS (JSON only)

    value     0..1; if missing and (n,total) given, computed as n/total
    n         items completed (any non-negative integer)
    total     total items (positive integer; 0 rejected)
    label     human-readable task label shown to viewers
    unit      progress unit (default "it")
    task_id   stable identifier for nested / parallel tasks
    event     one of: progress, start, end, label, url

LEARN MORE
    fernsicht run --help     all run-mode flags
    fernsicht doctor         diagnose installation
    fernsicht --help         top-level help

Privacy: no telemetry, no accounts. Magic-prefix lines never leave
your machine — they go to your local viewers via WebRTC.
`

// printMagicReference is split out so other places (help text, docs
// generators) can reuse it.
func printMagicReference(w io.Writer) {
	fmt.Fprint(w, magicReferenceText[1:]) // strip leading newline
}
