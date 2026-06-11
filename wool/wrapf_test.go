package wool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestWrapf_DoesNotInlineFields locks in the contract that structured fields
// attached via In(...)/With(...) are NOT dumped into the wrapped error
// message. Regression guard: a large struct attached as a field used to be
// serialized into the error string (via %v), burying the real cause behind
// noise and pushing it past the terminal width.
func TestWrapf_DoesNotInlineFields(t *testing.T) {
	type bigStruct struct {
		Type    string
		Service string
		Flag    bool
		Round   int
	}
	cause := errors.New("connection refused")
	w := Get(context.Background()).
		In("RuntimeStartPolicy.Execute", Field("action", bigStruct{"runtime-init", "infra/postgres", false, 0}))

	err := w.Wrapf(cause, "cannot process outputProperty")
	msg := err.Error()

	if strings.Contains(msg, "action=") || strings.Contains(msg, "runtime-init") {
		t.Fatalf("Wrapf inlined a structured field into the error message: %q", msg)
	}
	// The method name prefix and the human message must survive.
	if !strings.Contains(msg, "RuntimeStartPolicy.Execute") {
		t.Errorf("missing method prefix in %q", msg)
	}
	if !strings.Contains(msg, "cannot process outputProperty") {
		t.Errorf("missing wrap message in %q", msg)
	}
	// The root cause must remain reachable by walking the Unwrap chain.
	deepest := err
	for next := errors.Unwrap(deepest); next != nil; next = errors.Unwrap(deepest) {
		deepest = next
	}
	if deepest.Error() != "connection refused" {
		t.Errorf("root cause not preserved, deepest = %q", deepest.Error())
	}
}

// TestWrapf_NilNameNoPrefix verifies a fieldless, nameless wrap still wraps
// cleanly without spurious separators.
func TestWrapf_NilNameNoPrefix(t *testing.T) {
	cause := errors.New("boom")
	w := Get(context.Background())
	err := w.Wrapf(cause, "doing %s", "x")
	if !strings.Contains(err.Error(), "doing x") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected message: %q", err.Error())
	}
	// Sanity: fmt verb still applied.
	if strings.Contains(err.Error(), "%s") {
		t.Errorf("format verb not applied: %q", err.Error())
	}
	_ = fmt.Sprint(err)
}
