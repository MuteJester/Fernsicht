package cfg

import (
	"testing"
)

func TestFirstRun_IsTrueBeforeMark(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if !IsFirstRun() {
		t.Error("expected IsFirstRun() true on a fresh XDG_CONFIG_HOME")
	}
}

func TestFirstRun_FalseAfterMark(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := MarkFirstRunDone(); err != nil {
		t.Fatal(err)
	}
	if IsFirstRun() {
		t.Error("expected IsFirstRun() false after MarkFirstRunDone")
	}
}

func TestFirstRun_MarkIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := MarkFirstRunDone(); err != nil {
		t.Fatal(err)
	}
	if err := MarkFirstRunDone(); err != nil {
		t.Errorf("second MarkFirstRunDone should succeed; got %v", err)
	}
}
