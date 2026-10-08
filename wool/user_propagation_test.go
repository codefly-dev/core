package wool_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/wool"
)

// The package-level setters propagate; that is the whole point of them.
func TestWithOrgIDAndUserIDPropagateToALaterGet(t *testing.T) {
	ctx := wool.WithOrgID(context.Background(), "org-1")
	ctx = wool.WithUserID(ctx, "user-1")

	org, ok := wool.Get(ctx).OrgID()
	if !ok || org != "org-1" {
		t.Fatalf("OrgID() = (%q, %v), want (\"org-1\", true)", org, ok)
	}
	user, ok := wool.Get(ctx).UserID()
	if !ok || user != "user-1" {
		t.Fatalf("UserID() = (%q, %v), want (\"user-1\", true)", user, ok)
	}
}

// The method form does NOT, and this pins that difference rather than leaving
// the next caller to discover it from a value that reads back empty with no
// error. It is why the package-level form exists.
func TestTheMethodFormDoesNotReachTheContext(t *testing.T) {
	ctx := context.Background()

	wool.Get(ctx).WithOrgID("org-1")

	if org, ok := wool.Get(ctx).OrgID(); ok {
		t.Fatalf("the method form unexpectedly propagated: got %q", org)
	}
}

// An empty value binds nothing: a caller with no organization must not plant an
// empty one for a later reader to treat as present.
func TestAnEmptyIdentityBindsNothing(t *testing.T) {
	ctx := wool.WithOrgID(context.Background(), "")
	if _, ok := wool.Get(ctx).OrgID(); ok {
		t.Fatal("an empty organization must not be bound")
	}
	ctx = wool.WithUserID(context.Background(), "")
	if _, ok := wool.Get(ctx).UserID(); ok {
		t.Fatal("an empty user must not be bound")
	}
}

// A nil context is returned unchanged rather than panicking inside
// context.WithValue, matching how the rest of this package treats one.
func TestANilContextIsReturnedUnchanged(t *testing.T) {
	//nolint:staticcheck // deliberately passing a nil context
	if got := wool.WithOrgID(nil, "org-1"); got != nil {
		t.Fatal("a nil context must come back nil")
	}
}

// Binding one identity does not disturb the other, and a later bind overrides.
func TestIdentityKeysAreIndependentAndOverridable(t *testing.T) {
	ctx := wool.WithOrgID(context.Background(), "org-1")
	ctx = wool.WithUserID(ctx, "user-1")
	ctx = wool.WithOrgID(ctx, "org-2")

	if org, _ := wool.Get(ctx).OrgID(); org != "org-2" {
		t.Fatalf("OrgID() = %q, want the later value", org)
	}
	if user, _ := wool.Get(ctx).UserID(); user != "user-1" {
		t.Fatalf("UserID() = %q, want it undisturbed", user)
	}
}
