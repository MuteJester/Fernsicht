package parse

import "testing"

func TestTUI_FirstAltScreenWarns(t *testing.T) {
	var tui TUI
	if got := tui.HandleEvent(EventAltScreenEnter); !got {
		t.Errorf("first alt-screen enter should warn")
	}
	if !tui.Active() {
		t.Errorf("expected Active=true after enter")
	}
}

func TestTUI_SubsequentEnterDoesNotWarnAgain(t *testing.T) {
	var tui TUI
	tui.HandleEvent(EventAltScreenEnter)
	tui.HandleEvent(EventAltScreenExit)
	if got := tui.HandleEvent(EventAltScreenEnter); got {
		t.Errorf("second alt-screen enter should NOT warn")
	}
}

func TestTUI_ExitClearsActive(t *testing.T) {
	var tui TUI
	tui.HandleEvent(EventAltScreenEnter)
	tui.HandleEvent(EventAltScreenExit)
	if tui.Active() {
		t.Errorf("expected Active=false after exit")
	}
}

func TestTUI_OtherEventsIgnored(t *testing.T) {
	var tui TUI
	if got := tui.HandleEvent(EventNone); got {
		t.Errorf("EventNone should be a no-op")
	}
	if tui.Active() {
		t.Errorf("expected Active=false; got true")
	}
}
