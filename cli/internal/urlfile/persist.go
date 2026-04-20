// Package urlfile persists viewer URLs of currently-running sessions
// to a per-PID file, so users running headless can recover the URL
// later via `fernsicht url` (Phase 6) or by reading the file directly.
//
// Path: $XDG_RUNTIME_DIR/fernsicht/<pid>.url, falling back to
// /tmp/fernsicht-<pid>.url when XDG_RUNTIME_DIR is unset (e.g.,
// some headless / cron contexts).
package urlfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default returns the canonical URL-file path for pid. Honors
// $XDG_RUNTIME_DIR when present (the spec-defined per-user runtime
// directory cleared on logout — auto-cleanup of stale files).
//
// Fallback to /tmp keeps headless boxes (without a logged-in
// XDG session) workable, at the cost of needing manual cleanup of
// orphan files (`fernsicht url --all` Phase 6 reaps them).
func Default(pid int) string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "fernsicht", fmt.Sprintf("%d.url", pid))
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("fernsicht-%d.url", pid))
}

// Write atomically writes url to path. Creates parent directory
// (mode 0700 — runtime files are user-private).
//
// Atomicity: write to .tmp, then rename. Guarantees a reader sees
// either the previous contents or the full new contents, never a
// half-written line.
func Write(path, url string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("urlfile: mkdir %q: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(url+"\n"), 0o600); err != nil {
		return fmt.Errorf("urlfile: write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("urlfile: rename %q→%q: %w", tmp, path, err)
	}
	return nil
}

// Remove deletes the file at path. Returns nil if it doesn't exist
// (idempotent — graceful exit + reaper may both call this).
func Remove(path string) error {
	err := os.Remove(path)
	if err == nil || isNotExist(err) {
		return nil
	}
	return err
}

// Read returns the URL stored at path (the first non-empty line),
// stripped of whitespace. Returns ("", error) if the file doesn't
// exist or is unreadable.
func Read(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("urlfile: %q is empty", path)
}

// SessionEntry describes one running fernsicht session, as discovered
// by Discover().
type SessionEntry struct {
	PID  int
	Path string
	URL  string
}

// Discover walks the standard URL-file directory and returns one
// SessionEntry per file whose owning PID is still alive. Stale files
// (PID gone) are silently ignored — callers that want to reap them
// should call Remove on each.
//
// Used by `fernsicht url` (Phase 6) to enumerate running sessions.
func Discover() ([]SessionEntry, error) {
	dirs := candidateDirs()
	var out []SessionEntry
	seen := map[string]bool{}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if isNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			pid, ok := parsePIDFromName(name)
			if !ok {
				continue
			}
			path := filepath.Join(dir, name)
			if seen[path] {
				continue
			}
			seen[path] = true
			if !pidAlive(pid) {
				continue
			}
			url, err := Read(path)
			if err != nil {
				continue
			}
			out = append(out, SessionEntry{PID: pid, Path: path, URL: url})
		}
	}
	return out, nil
}

// candidateDirs returns the directories Discover scans, in priority
// order. Both XDG and /tmp paths are checked because a single host
// may have files written by sessions started under different
// contexts (some with XDG_RUNTIME_DIR, some without).
func candidateDirs() []string {
	dirs := []string{}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		dirs = append(dirs, filepath.Join(x, "fernsicht"))
	}
	dirs = append(dirs, os.TempDir())
	return dirs
}

// parsePIDFromName accepts both naming conventions:
//   - "<pid>.url" (XDG path)
//   - "fernsicht-<pid>.url" (tmp path)
func parsePIDFromName(name string) (int, bool) {
	if !strings.HasSuffix(name, ".url") {
		return 0, false
	}
	stem := strings.TrimSuffix(name, ".url")
	stem = strings.TrimPrefix(stem, "fernsicht-")
	pid, err := strconv.Atoi(stem)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// pidAlive reports whether a process with the given PID exists.
// Uses kill(pid, 0) which posts no signal but returns ESRCH if the
// process is gone.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; the real test is Signal(0).
	if err := proc.Signal(syscallSig0); err != nil {
		return false
	}
	return true
}

// isNotExist is a small wrapper so tests can stub.
func isNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || isPathError(err))
}

func isPathError(err error) bool {
	var pErr *fs.PathError
	if as(err, &pErr) {
		return os.IsNotExist(pErr.Err)
	}
	return false
}

// as is a tiny errors.As wrapper, kept here to avoid importing
// "errors" twice across this small file.
func as(err error, target any) bool {
	type unwrap interface{ Unwrap() error }
	if pe, ok := err.(*fs.PathError); ok {
		if t, ok := target.(**fs.PathError); ok {
			*t = pe
			return true
		}
	}
	return false
}
