package cfg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTOML(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, ".fernsicht.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ParsesFullSchema(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `
[run]
default_label = "{command}"
default_unit  = "step"
strict = true
qr = "always"

[detection]
disable_builtin = true
confidence_threshold_matches = 3
confidence_window_sec = 10

[[detection.patterns]]
name = "myorch"
regex = '\[ETA: (\d+):(\d+)\] (\d+)% complete'
value_capture = 3

[[detection.patterns]]
name = "thingfile"
regex = 'Wrote (\d+) of (\d+)'
n_capture = 1
total_capture = 2
`)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Run.DefaultUnit != "step" {
		t.Errorf("DefaultUnit got %q want step", f.Run.DefaultUnit)
	}
	if !f.Run.Strict {
		t.Errorf("Strict should be true")
	}
	if f.Run.QR != "always" {
		t.Errorf("QR got %q want always", f.Run.QR)
	}
	if !f.Detection.DisableBuiltin {
		t.Errorf("DisableBuiltin should be true")
	}
	if f.Detection.ConfidenceThresholdMatches != 3 {
		t.Errorf("ConfidenceThresholdMatches got %d want 3", f.Detection.ConfidenceThresholdMatches)
	}
	if len(f.Detection.Patterns) != 2 {
		t.Errorf("expected 2 patterns; got %d", len(f.Detection.Patterns))
	}
	p1 := f.Detection.Patterns[0]
	if p1.Name != "myorch" || p1.ValueCapture != 3 {
		t.Errorf("pattern 0: %+v", p1)
	}
	p2 := f.Detection.Patterns[1]
	if p2.NCapture != 1 || p2.TotalCapture != 2 {
		t.Errorf("pattern 1: %+v", p2)
	}
}

func TestLoad_MissingFileReturnsErrConfigNotFound(t *testing.T) {
	_, err := Load("/no/such/path.toml")
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("expected ErrConfigNotFound; got %v", err)
	}
}

func TestSearchAndLoad_ExplicitPathWins(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[run]
default_label = "explicit"`)
	f, src, err := SearchAndLoad(path)
	if err != nil {
		t.Fatal(err)
	}
	if src != path {
		t.Errorf("src got %q want %q", src, path)
	}
	if f.Run.DefaultLabel != "explicit" {
		t.Errorf("explicit path content not loaded")
	}
}

func TestSearchAndLoad_FindsInWorkingDir(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, `[run]
default_label = "from-cwd"`)

	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	os.Chdir(dir)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty; force walk-up not XDG

	f, src, err := SearchAndLoad("")
	if err != nil {
		t.Fatal(err)
	}
	if f.Run.DefaultLabel != "from-cwd" {
		t.Errorf("got %+v src=%q", f.Run, src)
	}
}

func TestSearchAndLoad_NoConfigReturnsNilNoError(t *testing.T) {
	dir := t.TempDir()
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	os.Chdir(dir)

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "no-config"))
	t.Setenv("HOME", dir) // bound walk-up search

	f, src, err := SearchAndLoad("")
	if err != nil {
		t.Errorf("no-config case should be err=nil; got %v", err)
	}
	if f != nil || src != "" {
		t.Errorf("no-config case should return nil; got f=%v src=%q", f, src)
	}
}

func TestSearchAndLoad_HonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "fernsicht")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte(`[run]
default_label = "from-xdg"`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	// Use a tempdir for cwd that has NO .fernsicht.toml.
	cwd := t.TempDir()
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	os.Chdir(cwd)
	t.Setenv("HOME", cwd) // bound walk-up

	f, src, err := SearchAndLoad("")
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatalf("expected XDG config to be found; src=%q", src)
	}
	if f.Run.DefaultLabel != "from-xdg" {
		t.Errorf("got %+v", f.Run)
	}
}
