package cfg

import (
	"fmt"
	"os"
	"path/filepath"
)

// firstRunMarkerPath returns the path to the file whose presence
// signals "this user has run fernsicht at least once before."
//
// Honors $XDG_CONFIG_HOME with the conventional ~/.config fallback
// (mirrors the spec at https://specifications.freedesktop.org/basedir-spec).
func firstRunMarkerPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cfg: cannot resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "fernsicht", "seen-intro"), nil
}

// IsFirstRun returns true if the marker doesn't exist (or can't be
// read). On error, we conservatively report "true" so the user gets
// the tip message — better than erroring on a quirk of their env.
func IsFirstRun() bool {
	path, err := firstRunMarkerPath()
	if err != nil {
		return true
	}
	_, err = os.Stat(path)
	return os.IsNotExist(err)
}

// MarkFirstRunDone touches the marker file so subsequent invocations
// suppress the tip. Errors are non-fatal — we degrade to "show tip
// every time" rather than fail to run.
func MarkFirstRunDone() error {
	path, err := firstRunMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cfg: mkdir %q: %w", filepath.Dir(path), err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cfg: create %q: %w", path, err)
	}
	return f.Close()
}
