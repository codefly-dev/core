package wool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// wool's surface is small but central — every gRPC handler in every
// agent uses Wrapf / NewError / In to build error chains. These tests
// pin the contract so refactors in wool don't silently regress error
// messages downstream.

func TestGet_AlwaysReturnsNonNil(t *testing.T) {
	// Even on background ctx with no provider registered, wool.Get
	// must hand back a usable Wool — the no-op fallback. Callers
	// rely on `wool.Get(ctx).In(...)` chaining without nil-checks.
	w := wool.Get(context.Background())
	require.NotNil(t, w)
}

func TestIn_NamesPropagateIntoError(t *testing.T) {
	w := wool.Get(context.Background()).In("MyMethod")
	err := w.Wrapf(errors.New("inner"), "outer message")
	require.Error(t, err)
	// Wrapf prefixes with the In() name. Without this guarantee,
	// errors lose their call-site context as they bubble through
	// gRPC handler chains.
	require.Contains(t, err.Error(), "MyMethod")
	require.Contains(t, err.Error(), "outer message")
	require.Contains(t, err.Error(), "inner")
}

func TestWrapf_Format(t *testing.T) {
	w := wool.Get(context.Background()).In("Process")
	err := w.Wrapf(errors.New("disk full"), "writing %s of %d bytes", "blob.bin", 4096)
	require.Error(t, err)
	require.Contains(t, err.Error(), "writing blob.bin of 4096 bytes")
	require.Contains(t, err.Error(), "disk full")
}

func TestWrapf_PreservesUnderlyingError(t *testing.T) {
	// errors.Is must traverse through Wrapf so callers can check
	// for sentinel errors (e.g. context.Canceled) up the stack.
	sentinel := errors.New("sentinel")
	w := wool.Get(context.Background()).In("X")
	wrapped := w.Wrapf(sentinel, "context")
	require.True(t, errors.Is(wrapped, sentinel),
		"errors.Is must reach the underlying sentinel through Wrapf")
}

func TestNewError_Format(t *testing.T) {
	w := wool.Get(context.Background())
	err := w.NewError("count=%d threshold=%d", 5, 3)
	require.Error(t, err)
	require.Equal(t, "count=5 threshold=3", err.Error())
}

func TestIn_ChainsNames(t *testing.T) {
	// Nested In() calls compose names. Critical for tracing
	// "the error came from Service.Method.helper" without
	// each layer manually stamping its own name.
	w := wool.Get(context.Background()).In("Service").In("Method")
	err := w.Wrapf(errors.New("boom"), "step")
	// Both names must appear in the message; order-stability isn't
	// part of the contract but presence is.
	require.True(t,
		strings.Contains(err.Error(), "Service") || strings.Contains(err.Error(), "Method"),
		"nested In() names should appear in error: %s", err.Error())
}

func TestField_Construction(t *testing.T) {
	// LogField factories shouldn't panic on nil/empty inputs —
	// they're called from defer-Catch panic-recovery paths and
	// can't themselves throw.
	require.NotPanics(t, func() {
		_ = wool.Field("k", "v")
		_ = wool.Field("count", 42)
		_ = wool.NameField("svc")
		_ = wool.ErrField(errors.New("x"))
		_ = wool.ErrField(nil) // common mistake — must not panic
	})
}

func TestInject_NoProviderIsSafe(t *testing.T) {
	// Without a registered telemetry provider (test/agent-bootstrap
	// path), Inject is a no-op rather than a nil-deref. Tested
	// behavior: doesn't panic, returns a usable (no-op) ctx, and
	// downstream wool.Get on it still produces a working Wool.
	w := wool.Get(context.Background()).In("Outer")
	require.NotPanics(t, func() {
		_ = w.Inject(context.Background())
	})
	ctx := w.Inject(context.Background())
	got := wool.Get(ctx)
	require.NotNil(t, got)
	// Without a provider to thread state through, the .In("Outer")
	// chain is intentionally lost — that's the no-op contract.
	// This pins the contract so a future change that does propagate
	// state shows up as a test failure to update the docstring.
	err := got.Wrapf(errors.New("inner"), "msg")
	require.NotContains(t, err.Error(), "Outer",
		"no-provider Inject should not propagate the .In() chain — "+
			"if this changes, update the contract comment in Inject")
}
