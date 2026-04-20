package clipboard

import (
	"errors"
	"testing"
)

func TestAvailable_DoesNotPanic(t *testing.T) {
	// We don't assert true/false — depends on test environment.
	// Just verify the call returns without crashing.
	_ = Available()
}

func TestCopy_GracefulOnMissingTool(t *testing.T) {
	if Available() {
		t.Skip("clipboard tool present in env; skipping missing-tool test")
	}
	err := Copy("text")
	if !errors.Is(err, ErrNoClipboard) {
		t.Errorf("expected ErrNoClipboard; got %v", err)
	}
}
