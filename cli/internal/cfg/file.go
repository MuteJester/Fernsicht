package cfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// File mirrors the .fernsicht.toml schema documented in CLI plan §5.3.
// Every field is optional; zero values mean "use built-in defaults."
type File struct {
	Run       RunSection       `toml:"run"`
	Detection DetectionSection `toml:"detection"`
}

// RunSection covers per-run defaults a user wants pre-set.
type RunSection struct {
	DefaultLabel         string `toml:"default_label"`
	DefaultUnit          string `toml:"default_unit"`
	RateLimitTicksPerSec int    `toml:"rate_limit_ticks_per_sec"`
	Strict               bool   `toml:"strict"`
	QR                   string `toml:"qr"` // "auto" | "always" | "never"
}

// DetectionSection covers parser tuning + custom Tier-3 patterns.
type DetectionSection struct {
	DisableBuiltin             bool             `toml:"disable_builtin"`
	ConfidenceThresholdMatches int              `toml:"confidence_threshold_matches"`
	ConfidenceWindowSec        int              `toml:"confidence_window_sec"`
	Patterns                   []PatternEntry   `toml:"patterns"`
}

// PatternEntry is one user-supplied custom regex.
type PatternEntry struct {
	Name         string `toml:"name"`
	Regex        string `toml:"regex"`
	ValueCapture int    `toml:"value_capture"` // 1-indexed
	NCapture     int    `toml:"n_capture"`
	TotalCapture int    `toml:"total_capture"`
}

// Load reads a .fernsicht.toml from path. Returns ErrConfigNotFound
// when the file doesn't exist; other errors propagate.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if isPathNotExist(err) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("cfg: read %q: %w", path, err)
	}
	var f File
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("cfg: parse %q: %w", path, err)
	}
	return &f, nil
}

// ErrConfigNotFound signals "no .fernsicht.toml at this path."
// Callers can use it to fall back to defaults silently.
var ErrConfigNotFound = errors.New("cfg: no config file found")

// SearchAndLoad implements the CLI plan §5.3 search order:
//
//   1. Explicit --config / FERNSICHT_CONFIG (already resolved by caller
//      and passed as `explicit`; empty means "no override")
//   2. ./.fernsicht.toml, walking up to $HOME
//   3. $XDG_CONFIG_HOME/fernsicht/config.toml (default ~/.config/...)
//
// Returns (config, sourcePath, nil) on success. (nil, "", nil) when
// nothing's found — caller falls back to defaults silently.
func SearchAndLoad(explicit string) (*File, string, error) {
	if explicit != "" {
		f, err := Load(explicit)
		if err != nil {
			return nil, "", err
		}
		return f, explicit, nil
	}

	// Walk up from cwd to $HOME (or until we hit /).
	cwd, err := os.Getwd()
	if err == nil {
		home, _ := os.UserHomeDir()
		dir := cwd
		for {
			path := filepath.Join(dir, ".fernsicht.toml")
			if f, err := Load(path); err == nil {
				return f, path, nil
			} else if !errors.Is(err, ErrConfigNotFound) {
				return nil, "", err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			if dir == home && home != "" {
				break
			}
			dir = parent
		}
	}

	// $XDG_CONFIG_HOME/fernsicht/config.toml (or ~/.config/fernsicht/config.toml)
	xdgPath := xdgConfigPath()
	if xdgPath != "" {
		f, err := Load(xdgPath)
		if err == nil {
			return f, xdgPath, nil
		}
		if !errors.Is(err, ErrConfigNotFound) {
			return nil, "", err
		}
	}

	return nil, "", nil
}

func xdgConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "fernsicht", "config.toml")
}

func isPathNotExist(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	var pErr *fs.PathError
	if errors.As(err, &pErr) {
		return os.IsNotExist(pErr.Err)
	}
	return false
}
