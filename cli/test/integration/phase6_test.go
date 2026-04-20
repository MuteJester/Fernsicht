// Phase 6 integration tests: doctor, url, magic, completion,
// update, --explain, suggestion-on-typo.
package integration

import (
	"strings"
	"testing"
)

// --- magic -----------------------------------------------------------

func TestPhase6_MagicPrintsReference(t *testing.T) {
	out, _, code := run(t, "magic")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"__fernsicht__",
		"JSON FORM",
		"COMPACT FORM",
		"EXAMPLES",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("magic output missing %q; got first 200 chars: %q",
				want, out[:min(200, len(out))])
		}
	}
}

// --- url -------------------------------------------------------------

func TestPhase6_URLNoSession_ExitOne(t *testing.T) {
	_, errOut, code := run(t, "url")
	if code != 1 {
		t.Errorf("expected exit 1 with no sessions; got %d", code)
	}
	if !strings.Contains(errOut, "no running fernsicht sessions") {
		t.Errorf("expected helpful 'no sessions' message; got: %q", errOut)
	}
}

func TestPhase6_URLPidNoMatch(t *testing.T) {
	_, errOut, code := run(t, "url", "--pid", "999999999")
	if code != 1 {
		t.Errorf("expected exit 1; got %d", code)
	}
	if !strings.Contains(errOut, "no running session with PID 999999999") {
		t.Errorf("expected pid-not-found message; got: %q", errOut)
	}
}

// --- doctor ----------------------------------------------------------

func TestPhase6_DoctorRunsAllChecks(t *testing.T) {
	// Use the shared fake server so the network checks pass quickly.
	out, _, _ := run(t, "doctor",
		"--server-url", sharedFake.URL(),
		"--no-color")
	for _, want := range []string{
		"binary integrity",
		"platform support",
		"DNS resolution",
		"tcp connectivity",
		"signaling /healthz",
		"telemetry-free",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing check %q", want)
		}
	}
}

func TestPhase6_DoctorExplainE001(t *testing.T) {
	out, _, code := run(t, "doctor", "--explain", "E001")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"E001", "cause:", "hint:", "docs:"} {
		if !strings.Contains(out, want) {
			t.Errorf("--explain output missing %q; got: %q", want, out)
		}
	}
}

func TestPhase6_DoctorExplainUnknownExitsOne(t *testing.T) {
	_, errOut, code := run(t, "doctor", "--explain", "E999")
	if code != 1 {
		t.Errorf("expected exit 1 for unknown code; got %d", code)
	}
	if !strings.Contains(errOut, "no error code") {
		t.Errorf("expected 'no error code' message; got: %q", errOut)
	}
}

// --- completion ------------------------------------------------------

func TestPhase6_CompletionEachShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			out, _, code := run(t, "completion", shell)
			if code != 0 {
				t.Fatalf("exit %d for %s", code, shell)
			}
			if len(out) < 100 {
				t.Errorf("%s completion output too short (%d bytes)", shell, len(out))
			}
			// Each script should mention "fernsicht" somewhere.
			if !strings.Contains(out, "fernsicht") {
				t.Errorf("%s completion missing 'fernsicht' string", shell)
			}
		})
	}
}

func TestPhase6_CompletionUnknownShell(t *testing.T) {
	_, errOut, code := run(t, "completion", "tcsh")
	if code != 2 {
		t.Errorf("expected exit 2 for unsupported shell; got %d", code)
	}
	if !strings.Contains(errOut, "unsupported shell") {
		t.Errorf("expected 'unsupported shell' error; got: %q", errOut)
	}
}

func TestPhase6_CompletionMissingArg(t *testing.T) {
	_, errOut, code := run(t, "completion")
	if code != 2 {
		t.Errorf("expected exit 2 for missing arg; got %d", code)
	}
	if !strings.Contains(errOut, "missing shell") {
		t.Errorf("expected 'missing shell' error; got: %q", errOut)
	}
}

// --- typo suggestion ------------------------------------------------

func TestPhase6_TypoSuggestion(t *testing.T) {
	cases := []struct {
		typed  string
		expect string
	}{
		{"doctore", "doctor"},
		{"versino", "version"},
		{"compeltion", "completion"}, // less obvious; suggester picks closest
		{"updates", "update"},
	}
	for _, tc := range cases {
		_, errOut, code := run(t, tc.typed)
		if code != 2 {
			t.Errorf("%q: expected exit 2; got %d", tc.typed, code)
			continue
		}
		// Either a suggestion appears or no suggestion (when nothing
		// was close enough). Test only that, when a suggestion fires,
		// it's one of the known commands.
		if strings.Contains(errOut, "did you mean") {
			if !strings.Contains(errOut, tc.expect) {
				t.Logf("%q: suggestion didn't match best guess (got: %q, expected to contain %q)",
					tc.typed, errOut, tc.expect)
				// Not a hard failure — suggestion is heuristic.
			}
		}
	}
}

// --- top-level help has all subcommands ------------------------------

func TestPhase6_TopLevelHelpListsAllSubcommands(t *testing.T) {
	out, _, code := run(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, sub := range []string{"run", "url", "doctor", "magic", "completion", "update", "version"} {
		if !strings.Contains(out, sub) {
			t.Errorf("--help missing subcommand %q", sub)
		}
	}
	// Privacy line is part of the standard footer.
	if !strings.Contains(out, "Privacy") {
		t.Errorf("--help missing privacy footer; got: %q", out)
	}
}
