package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// updateCommand handles `fernsicht update [--check]`.
//
// --check (Phase 6 default): query the GitHub releases API, compare
// against this binary's version, print one of:
//
//   - "you're on the latest" (no action needed)
//   - "newer version available: vX.Y.Z" (instructions to upgrade)
//
// Without --check (Phase 7+ feature): runs the install one-liner.
// For Phase 6 we just print the install command — auto-update
// mechanisms have a long history of bugs (partial downloads, locked
// binaries on Windows, signing-cert rotations) so we'd rather have
// users explicitly re-run install.sh.
func updateCommand(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "Check only, don't install.")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"[fernsicht] error: could not check for updates: %v\n", err)
		return 1
	}

	current := normalizeVersion(version)
	latestNorm := normalizeVersion(latest)

	if current == "" || latestNorm == "" {
		fmt.Printf("current: %s\nlatest:  %s\n(could not compare versions cleanly; check manually)\n",
			version, latest)
		return 0
	}

	if current == latestNorm {
		fmt.Printf("✓ You're on the latest fernsicht (%s).\n", current)
		return 0
	}

	cmp := compareVersions(current, latestNorm)
	if cmp >= 0 {
		fmt.Printf("✓ You're on a newer version (%s) than the latest published (%s).\n",
			current, latestNorm)
		return 0
	}

	fmt.Printf("→ Newer version available: %s (you have %s)\n\n", latestNorm, current)
	if *check {
		fmt.Printf("To upgrade, re-run the install one-liner:\n")
	} else {
		fmt.Printf("Auto-install isn't supported in this version. To upgrade:\n")
	}
	fmt.Println()
	fmt.Println("  curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh")
	fmt.Println()
	fmt.Println("  # or for Windows:")
	fmt.Println("  irm https://github.com/MuteJester/Fernsicht/releases/latest/download/install.ps1 | iex")
	return 0
}

// fetchLatestVersion queries GitHub for the latest release tag and
// returns it without the "cli/v" prefix (so semver comparison is
// straightforward).
//
// Test seam: githubAPIURL is overridable.
var githubAPIURL = "https://api.github.com/repos/MuteJester/Fernsicht/releases"

func fetchLatestVersion(ctx context.Context) (string, error) {
	// Use the /releases endpoint (not /releases/latest) so we can
	// filter out pre-releases manually — GitHub's "latest" treats
	// pre-releases as latest if they're the most recent commit-wise,
	// which we don't want.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("github API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var releases []struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("parse releases JSON: %w", err)
	}

	for _, r := range releases {
		if r.Prerelease || r.Draft {
			continue
		}
		if !strings.HasPrefix(r.TagName, "cli/v") {
			continue
		}
		return strings.TrimPrefix(r.TagName, "cli/v"), nil
	}
	return "", fmt.Errorf("no stable cli/v* releases found")
}

// normalizeVersion strips common prefixes so "v0.1.0", "0.1.0",
// "cli/v0.1.0" all become "0.1.0".
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "cli/")
	v = strings.TrimPrefix(v, "v")
	// Strip "-dev" / "-rcN" suffixes for the lookup display, but keep
	// them for compareVersions decisions (handled there).
	if i := strings.IndexAny(v, " -+"); i > 0 && !strings.HasPrefix(v[i:], "-") {
		v = v[:i]
	}
	return v
}

// compareVersions returns -1, 0, +1 like strings.Compare, on a
// best-effort semver comparison. Pre-release suffixes after "-" are
// treated as "less than" the base version (rc < 0 < release).
func compareVersions(a, b string) int {
	stripPre := func(s string) (base, pre string) {
		if i := strings.Index(s, "-"); i > 0 {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	ab, apre := stripPre(a)
	bb, bpre := stripPre(b)
	if c := compareSemverNumeric(ab, bb); c != 0 {
		return c
	}
	// Same base. Pre-release sorts BEFORE release.
	if apre == "" && bpre != "" {
		return 1
	}
	if apre != "" && bpre == "" {
		return -1
	}
	return strings.Compare(apre, bpre)
}

func compareSemverNumeric(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			fmt.Sscanf(as[i], "%d", &av)
		}
		if i < len(bs) {
			fmt.Sscanf(bs[i], "%d", &bv)
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}
