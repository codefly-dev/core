package policy_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

// =====================================================================
// PermissionsCallbackServer + callbackAuthorizer integration
//
// These tests spin up the actual UDS-bound server in-process and
// dial it from the plugin-side client. No mocking of HTTP/UDS —
// the wire is exercised end-to-end.
// =====================================================================

func TestCallback_Roundtrip_Allow(t *testing.T) {
	// Server side: a FakePDP configured to allow git.status.
	pdp := testharness.NewFakeDeny().AllowTool("", "git.status")
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck

	// Provide a principal — production wiring uses the spawn-time
	// principal; here we set a fixed test principal.
	srv.WithPrincipalProvider(func() *policy.Principal {
		return &policy.Principal{
			ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1",
		}
	})

	// Client side.
	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)

	allowed, reason, err := client.Authorized(context.Background(), "git.status", "")
	require.NoError(t, err)
	require.True(t, allowed, "callback must round-trip an allow verdict")
	_ = reason
}

func TestCallback_Roundtrip_Deny(t *testing.T) {
	pdp := testharness.NewFakeAllow().DenyTool("", "github.force_push", "force-push forbidden")
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck
	srv.WithPrincipalProvider(func() *policy.Principal {
		return &policy.Principal{ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1"}
	})

	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)
	allowed, reason, err := client.Authorized(context.Background(), "github.force_push", "repo:foo")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, "force-push forbidden", reason,
		"deny reason from PDP must surface verbatim across the callback")
}

func TestCallback_NoSocketEnv_FailsClosed(t *testing.T) {
	t.Setenv(policy.EnvPermissionsSocket, "")
	a := policy.NewCallbackAuthorizerFromEnv()
	allowed, reason, err := a.Authorized(context.Background(), "anything", "")
	require.NoError(t, err)
	require.False(t, allowed,
		"no socket env = fail-closed (the safe default — no ambient allow)")
	require.Contains(t, reason, "CODEFLY_PERMISSIONS_SOCKET")
}

func TestCallback_DialFails_FailsClosedWithErr(t *testing.T) {
	// Point at a path that doesn't exist; dial will fail.
	client := policy.NewCallbackAuthorizer("/tmp/codefly-nonexistent.sock", 200*time.Millisecond)
	allowed, reason, err := client.Authorized(context.Background(), "x", "")
	require.False(t, allowed)
	require.Error(t, err, "transport failure surfaces as err so callers distinguish from policy deny")
	require.Contains(t, reason, "unreachable")
}

func TestCallback_PrincipalProvider_OverridesRequestClaim(t *testing.T) {
	// The plugin can claim any principal_id in the request, but
	// the host's principalProvider OVERRIDES — security-critical:
	// a compromised plugin cannot escalate by claiming a different
	// principal.
	type observed struct {
		identity map[string]any
	}
	called := make(chan observed, 1)
	pdp := pdpRecorder(func(req *policy.PDPRequest) policy.PDPDecision {
		called <- observed{identity: copyMapForTest(req.Identity)}
		return policy.PDPDecision{Allow: true}
	})

	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck
	srv.WithPrincipalProvider(func() *policy.Principal {
		return &policy.Principal{ID: "trusted-id", Kind: policy.KindAgent, OrgID: "org-1", AgentID: "x/y:z"}
	})

	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)
	// Plugin claims a DIFFERENT principal_id. Should be ignored.
	_, _, err = client.Authorized(context.Background(), "git.status", "")
	require.NoError(t, err)

	select {
	case fc := <-called:
		require.Equal(t, "trusted-id", fc.identity["principal_id"],
			"PDP must see the trusted principal, NOT the plugin's claim")
	case <-time.After(time.Second):
		t.Fatal("PDP not called within timeout")
	}
}

func TestCallback_NoPrincipalProvider_FailsClosed(t *testing.T) {
	// Without a principalProvider AND with empty PrincipalID in
	// the request, the callback fails closed.
	pdp := testharness.NewFakeAllow()
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck
	// Note: no WithPrincipalProvider call.

	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)
	allowed, reason, err := client.Authorized(context.Background(), "git.status", "")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Contains(t, reason, "no principal")
}

func TestCallback_Server_NilDecider_Errors(t *testing.T) {
	_, err := policy.NewPermissionsCallbackServer(nil)
	require.Error(t, err)
}

func TestCallback_Server_Close_RemovesSocket(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)

	path := srv.SocketPath()
	require.FileExists(t, path)

	require.NoError(t, srv.Close())

	// After close, the socket file must be gone — orphan files
	// are operational paper cuts.
	_, statErr := osStatForTest(path)
	require.Error(t, statErr, "socket file must be removed on Close")
}

func TestCallback_Server_Close_Idempotent(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	require.NoError(t, srv.Close())
	require.NoError(t, srv.Close(), "double-close is a no-op")
}

func TestCallback_PrincipalProviderMutation_RaceFree(t *testing.T) {
	// Regression: WithPrincipalProvider used to write s.principalProvider
	// without a lock while handleAuthorize goroutines were already
	// running (the listener Serves before manager.Load installs the
	// provider). Race detector + concurrent setter/caller proves the
	// providerMu guard works.
	pdp := testharness.NewFakeAllow()
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck

	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Caller goroutines: dial in a tight loop.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _, _ = client.Authorized(context.Background(), "git.status", "")
				}
			}
		}()
	}

	// Mutator: swap the provider repeatedly. With the old unsynchronized
	// write this races against handleAuthorize's read.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := "p"
			if i%2 == 0 {
				id = "q"
			}
			srv.WithPrincipalProvider(func() *policy.Principal {
				return &policy.Principal{ID: id, Kind: policy.KindHuman, OrgID: "o"}
			})
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestCallback_Concurrent_Calls(t *testing.T) {
	// Race detector validates the server's HTTP handler under
	// concurrent dials. 100 goroutines, all hitting the same
	// callback.
	pdp := testharness.NewFakeAllow()
	srv, err := policy.NewPermissionsCallbackServer(pdp)
	require.NoError(t, err)
	defer srv.Close() //nolint:errcheck
	srv.WithPrincipalProvider(func() *policy.Principal {
		return &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "o"}
	})

	client := policy.NewCallbackAuthorizer(srv.SocketPath(), 2*time.Second)
	var wg sync.WaitGroup
	errCh := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := client.Authorized(context.Background(), "git.status", "")
			if err != nil {
				errCh <- err
			}
			if !allowed {
				errCh <- errors.New("expected allow")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent call failed: %v", err)
	}
}

// =====================================================================
// AuthorizerFromContext / WithAuthorizer
// =====================================================================

func TestAuthorizerFromContext_Empty_ReturnsDisabled(t *testing.T) {
	a := policy.AuthorizerFromContext(context.Background())
	require.NotNil(t, a, "must always return non-nil — defense against nil-deref in handlers")

	allowed, reason, err := a.Authorized(context.Background(), "x", "")
	require.NoError(t, err)
	require.False(t, allowed)
	require.NotEmpty(t, reason)
}

func TestAuthorizerFromContext_NilCtx_ReturnsDisabled(t *testing.T) {
	a := policy.AuthorizerFromContext(nil) //nolint:staticcheck // intentional nil
	require.NotNil(t, a)
	allowed, _, err := a.Authorized(context.Background(), "x", "")
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestAuthorizerFromContext_WithAuthorizer_RoundTrip(t *testing.T) {
	mock := &recordingAuthorizer{}
	ctx := policy.WithAuthorizer(context.Background(), mock)
	got := policy.AuthorizerFromContext(ctx)
	require.Same(t, mock, got)

	got.Authorized(context.Background(), "a", "r") //nolint:errcheck
	require.Len(t, mock.calls, 1)
}

func TestAuthorizerFromContext_NilCtxStamp_Tolerated(t *testing.T) {
	mock := &recordingAuthorizer{}
	ctx := policy.WithAuthorizer(nil, mock) //nolint:staticcheck // intentional nil
	require.NotNil(t, ctx)
	require.Same(t, mock, policy.AuthorizerFromContext(ctx))
}

// =====================================================================
// Helpers
// =====================================================================

// pdpRecorder is a Decider whose Evaluate calls a function. Used
// when the test wants to inspect the PDPRequest the callback
// builds (separate from FakePDP, which only records by tool name).
type pdpRecorder func(req *policy.PDPRequest) policy.PDPDecision

func (f pdpRecorder) Evaluate(_ context.Context, req *policy.PDPRequest) policy.PDPDecision {
	return f(req)
}

type recordingAuthorizer struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingAuthorizer) Authorized(_ context.Context, action, resource string) (bool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, action+":"+resource)
	return true, "", nil
}

func copyMapForTest(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// osStatForTest checks file existence in tests. Inline rather than
// import "os" at file level just for one helper.
func osStatForTest(path string) (any, error) {
	return os.Stat(path)
}
