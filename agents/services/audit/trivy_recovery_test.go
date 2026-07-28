package audit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func TestAuditWithTrivyDatabaseRecoveryCoordinatesParallelCallers(t *testing.T) {
	coordinator := newTrivyAuditRecoveryCoordinator()
	request := &builderv0.AuditRequest{}
	clean := &builderv0.AuditResponse{
		State: &builderv0.AuditStatus{State: builderv0.AuditStatus_CLEAN},
		Tool:  "trivy",
	}

	const callers = 3
	initialCalls := make(chan struct{}, callers)
	releaseInitialCalls := make(chan struct{})
	var resetCalls atomic.Int32
	reset := func(context.Context) error {
		resetCalls.Add(1)
		return nil
	}

	results := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			calls := 0
			audit := func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
				calls++
				if calls == 1 {
					initialCalls <- struct{}{}
					<-releaseInitialCalls
					return nil, corruptTrivyDownloadTestError()
				}
				return clean, nil
			}
			response, err := coordinator.audit(context.Background(), request, audit, reset)
			if err == nil && response != clean {
				err = errors.New("recovery returned the wrong audit response")
			}
			results <- err
		}()
	}
	for range callers {
		<-initialCalls
	}
	close(releaseInitialCalls)
	group.Wait()
	close(results)

	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, resetCalls.Load(), "one failed cache generation must be reset once")
}

func TestAuditWithTrivyDatabaseRecoveryMatchesOwnedDatabaseFailures(t *testing.T) {
	tests := []string{
		"builder audit failed: trivy audit failed: FATAL DB error: failed to download vulnerability DB",
		"builder audit failed\nfatal error: fault\n[signal SIGSEGV]\ngo.etcd.io/bbolt/internal/common.(*Page).FastCheck\ngithub.com/aquasecurity/trivy-db/pkg/db.Config.forEach",
		"builder audit failed\nfatal error: fault\ngo.etcd.io/bbolt/internal/common.(*Page).FastCheck\ngithub.com/aquasecurity/trivy-java-db/pkg/db.Config.Get",
	}
	for _, message := range tests {
		t.Run(message, func(t *testing.T) {
			coordinator := newTrivyAuditRecoveryCoordinator()
			calls := 0
			audit := func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
				calls++
				if calls == 1 {
					return nil, errors.New(message)
				}
				return &builderv0.AuditResponse{Tool: "trivy"}, nil
			}
			resets := 0

			response, err := coordinator.audit(context.Background(), &builderv0.AuditRequest{}, audit, func(context.Context) error {
				resets++
				return nil
			})

			require.NoError(t, err)
			require.Equal(t, "trivy", response.GetTool())
			require.Equal(t, 2, calls)
			require.Equal(t, 1, resets)
		})
	}
}

func TestAuditWithTrivyDatabaseRecoveryLeavesUnownedFailuresUntouched(t *testing.T) {
	tests := []string{
		"builder audit failed: another scanner failed to download vulnerability DB",
		"builder audit failed: trivy audit failed: signal SIGSEGV",
		"builder audit failed: go.etcd.io/bbolt/internal/common.(*Page).FastCheck",
		"builder audit failed: bbolt FastCheck: github.com/aquasecurity/trivy/pkg/cache",
	}
	for _, message := range tests {
		t.Run(message, func(t *testing.T) {
			coordinator := newTrivyAuditRecoveryCoordinator()
			auditErr := errors.New(message)
			calls := 0
			audit := func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
				calls++
				return nil, auditErr
			}
			resets := 0

			_, err := coordinator.audit(context.Background(), &builderv0.AuditRequest{}, audit, func(context.Context) error {
				resets++
				return nil
			})

			require.ErrorIs(t, err, auditErr)
			require.Equal(t, 1, calls)
			require.Zero(t, resets)
		})
	}
}

func TestAuditWithTrivyDatabaseRecoveryPreservesResetFailure(t *testing.T) {
	coordinator := newTrivyAuditRecoveryCoordinator()
	auditErr := corruptTrivyDownloadTestError()
	resetErr := errors.New("permission denied removing root-owned database")
	calls := 0

	_, err := coordinator.audit(
		context.Background(),
		&builderv0.AuditRequest{},
		func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
			calls++
			return nil, auditErr
		},
		func(context.Context) error { return resetErr },
	)

	require.ErrorIs(t, err, auditErr)
	require.ErrorIs(t, err, resetErr)
	require.Equal(t, 1, calls, "a failed reset must not retry against the known-corrupt database")
}

func TestAuditWithTrivyDatabaseRecoveryCleansCorruptRetry(t *testing.T) {
	coordinator := newTrivyAuditRecoveryCoordinator()
	auditErr := corruptTrivyDownloadTestError()
	calls := 0
	resets := 0

	_, err := coordinator.audit(
		context.Background(),
		&builderv0.AuditRequest{},
		func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
			calls++
			return nil, auditErr
		},
		func(context.Context) error {
			resets++
			return nil
		},
	)

	require.ErrorIs(t, err, auditErr)
	require.Equal(t, 2, calls)
	require.Equal(t, 2, resets)
}

func corruptTrivyDownloadTestError() error {
	return errors.New("builder audit failed: trivy audit failed: failed to download vulnerability DB")
}
