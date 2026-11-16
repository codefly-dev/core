package proto

import (
	"context"
	"time"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"
)

// `buf dep update` resolves buf.yaml's dependencies against the Buf Schema
// Registry, the one generation step that needs a remote. The registry answers
// Unavailable now and then ("the server hosted at that remote is
// unavailable"), and a single such answer failed a whole sync, so the step is
// retried a bounded number of times. Retrying is safe because the step is
// idempotent: it only rewrites buf.lock from buf.yaml. A failure that persists
// still fails, with its last error, after depUpdateAttempts runs.
const depUpdateAttempts = 3

// depUpdateBackoff is the wait before the second run; each later wait grows by
// the same step. A variable so a test does not sleep.
var depUpdateBackoff = 2 * time.Second

// processStarter is the part of a companion runner updateBufDependencies uses.
type processStarter interface {
	NewProcess(bin string, args ...string) (base.Proc, error)
}

// updateBufDependencies runs `buf dep update` in the runner's working
// directory, retrying a failed run.
func updateBufDependencies(ctx context.Context, runner processStarter) error {
	w := wool.Get(ctx).In("proto.updateBufDependencies")
	for attempt := 1; ; attempt++ {
		proc, err := runner.NewProcess("buf", "dep", "update")
		if err != nil {
			return w.Wrapf(err, "cannot create process")
		}
		err = proc.Run(ctx)
		if err == nil {
			return nil
		}
		if attempt == depUpdateAttempts || ctx.Err() != nil {
			return w.Wrapf(err, "cannot update buf")
		}
		delay := depUpdateBackoff * time.Duration(attempt)
		w.Warn("buf dep update failed; retrying",
			wool.ErrField(err), wool.Field("attempt", attempt), wool.Field("retry_in", delay.String()))
		select {
		case <-ctx.Done():
			return w.Wrapf(err, "cannot update buf")
		case <-time.After(delay):
		}
	}
}
