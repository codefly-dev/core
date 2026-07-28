package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// BuilderAuditFunc is the Builder.Audit contract used by
// AuditWithTrivyDatabaseRecovery.
type BuilderAuditFunc func(context.Context, *builderv0.AuditRequest) (*builderv0.AuditResponse, error)

var sharedTrivyAuditRecovery = newTrivyAuditRecoveryCoordinator()

// AuditWithTrivyDatabaseRecovery runs a builder audit and, when a
// Trivy-owned database failure is reported, resets the shared databases and
// retries once. Parallel callers share one recovery epoch: every audit already
// using the failed database finishes before one reset occurs, and new audits
// wait for that reset instead of deleting a database another caller repaired.
func AuditWithTrivyDatabaseRecovery(ctx context.Context, request *builderv0.AuditRequest, audit BuilderAuditFunc) (*builderv0.AuditResponse, error) {
	return sharedTrivyAuditRecovery.audit(ctx, request, audit, resetTrivyDatabases)
}

type trivyAuditRecoveryCoordinator struct {
	mu      sync.Mutex
	current *trivyAuditEpoch
}

type trivyAuditEpoch struct {
	active   int
	changed  chan struct{}
	recovery *trivyRecoveryAttempt
}

type trivyRecoveryAttempt struct {
	done chan struct{}
	err  error
}

func newTrivyAuditRecoveryCoordinator() *trivyAuditRecoveryCoordinator {
	return &trivyAuditRecoveryCoordinator{current: newTrivyAuditEpoch()}
}

func newTrivyAuditEpoch() *trivyAuditEpoch {
	return &trivyAuditEpoch{changed: make(chan struct{})}
}

func (c *trivyAuditRecoveryCoordinator) audit(
	ctx context.Context,
	request *builderv0.AuditRequest,
	audit BuilderAuditFunc,
	reset func(context.Context) error,
) (*builderv0.AuditResponse, error) {
	response, auditErr, epoch := c.run(ctx, request, audit)
	if auditErr == nil || !isCorruptTrivyDatabaseError(auditErr) {
		return response, auditErr
	}
	if resetErr := c.recover(ctx, epoch, reset); resetErr != nil {
		return response, fmt.Errorf("recover corrupt Trivy database: %w", errors.Join(
			auditErr,
			fmt.Errorf("reset Trivy databases: %w", resetErr),
		))
	}

	retryResponse, retryErr, retryEpoch := c.run(ctx, request, audit)
	if retryErr == nil {
		return retryResponse, nil
	}
	retryErr = fmt.Errorf("audit retry after resetting corrupt Trivy database: %w", retryErr)
	if !isCorruptTrivyDatabaseError(retryErr) {
		return retryResponse, retryErr
	}
	if resetErr := c.recover(ctx, retryEpoch, reset); resetErr != nil {
		return retryResponse, errors.Join(
			retryErr,
			fmt.Errorf("reset Trivy databases after failed retry: %w", resetErr),
		)
	}
	return retryResponse, fmt.Errorf("Trivy database was reset again after a corrupt audit retry: %w", retryErr)
}

func (c *trivyAuditRecoveryCoordinator) run(
	ctx context.Context,
	request *builderv0.AuditRequest,
	audit BuilderAuditFunc,
) (*builderv0.AuditResponse, error, *trivyAuditEpoch) {
	epoch, err := c.begin(ctx)
	if err != nil {
		return nil, err, nil
	}
	response, auditErr := audit(ctx, request)
	c.finish(epoch)
	return response, auditErr, epoch
}

func (c *trivyAuditRecoveryCoordinator) begin(ctx context.Context) (*trivyAuditEpoch, error) {
	for {
		c.mu.Lock()
		epoch := c.current
		if epoch.recovery == nil {
			epoch.active++
			c.mu.Unlock()
			return epoch, nil
		}
		done := epoch.recovery.done
		c.mu.Unlock()

		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *trivyAuditRecoveryCoordinator) finish(epoch *trivyAuditEpoch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	epoch.active--
	c.signalLocked(epoch)
}

func (c *trivyAuditRecoveryCoordinator) recover(ctx context.Context, epoch *trivyAuditEpoch, reset func(context.Context) error) error {
	c.mu.Lock()
	if epoch.recovery != nil {
		attempt := epoch.recovery
		c.mu.Unlock()
		return waitForTrivyRecovery(ctx, attempt)
	}

	attempt := &trivyRecoveryAttempt{done: make(chan struct{})}
	epoch.recovery = attempt
	c.signalLocked(epoch)
	for epoch.active > 0 {
		changed := epoch.changed
		c.mu.Unlock()
		select {
		case <-changed:
			c.mu.Lock()
		case <-ctx.Done():
			c.mu.Lock()
			c.completeRecoveryLocked(epoch, attempt, ctx.Err())
			return ctx.Err()
		}
	}
	c.mu.Unlock()

	resetErr := reset(ctx)
	c.mu.Lock()
	c.completeRecoveryLocked(epoch, attempt, resetErr)
	return resetErr
}

func (c *trivyAuditRecoveryCoordinator) completeRecoveryLocked(epoch *trivyAuditEpoch, attempt *trivyRecoveryAttempt, err error) {
	attempt.err = err
	if c.current == epoch {
		c.current = newTrivyAuditEpoch()
	}
	close(attempt.done)
	c.mu.Unlock()
}

func (c *trivyAuditRecoveryCoordinator) signalLocked(epoch *trivyAuditEpoch) {
	close(epoch.changed)
	epoch.changed = make(chan struct{})
}

func waitForTrivyRecovery(ctx context.Context, attempt *trivyRecoveryAttempt) error {
	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isCorruptTrivyDatabaseError recognizes only failures owned by Trivy's
// vulnerability-database implementations. The download wording alone is not
// sufficient because Builder.Audit can be implemented by unrelated scanners.
func isCorruptTrivyDatabaseError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "trivy audit failed") &&
		strings.Contains(message, "failed to download vulnerability db") {
		return true
	}
	if !strings.Contains(message, "bbolt") || !strings.Contains(message, "fastcheck") {
		return false
	}
	return strings.Contains(message, "github.com/aquasecurity/trivy-db/") ||
		strings.Contains(message, "github.com/aquasecurity/trivy-java-db/")
}
