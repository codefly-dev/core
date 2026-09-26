package workcontext

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ReplayStore records the consumption of single-use capabilities. A verifier
// cannot enforce single-use without one, which is why Verifier requires it
// rather than treating its absence as "replay checking off".
type ReplayStore interface {
	// Consume records that nonce has been spent and must not be accepted
	// again before retainUntil. It returns ErrReplayed when the nonce was
	// already consumed.
	Consume(ctx context.Context, nonce string, retainUntil time.Time) error
}

// MemoryReplayStore is the in-process store. It is enough for a single
// verifier and for tests; a deployment that verifies the same capability from
// more than one process needs a shared one.
type MemoryReplayStore struct {
	// Now is the clock the retention sweep reads. nil means time.Now.
	// A store sweeping on a different clock than the verifier forgets
	// capabilities the verifier still considers live, so the two must
	// agree.
	Now func() time.Time

	mu        sync.Mutex
	seen      map[string]time.Time
	nextSweep time.Time
}

// sweepInterval bounds how often expired nonces are collected. Sweeping on
// every call would put an O(n) scan under the lock on the hot path of every
// verification, which a deployment that makes ordinary sessions single-use
// would feel as serialized latency across all of them.
const sweepInterval = time.Second

// NewMemoryReplayStore returns an empty store using the wall clock.
func NewMemoryReplayStore() *MemoryReplayStore {
	return &MemoryReplayStore{seen: map[string]time.Time{}}
}

// Consume implements ReplayStore.
func (s *MemoryReplayStore) Consume(_ context.Context, nonce string, retainUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if !now.Before(s.nextSweep) {
		for spent, until := range s.seen {
			if until.Before(now) {
				delete(s.seen, spent)
			}
		}
		s.nextSweep = now.Add(sweepInterval)
	}
	// Retention is what makes a nonce refusable, so an entry the sweep has
	// not reached yet still counts: expiry is checked here rather than left
	// to collection.
	if until, spent := s.seen[nonce]; spent && !until.Before(now) {
		return fmt.Errorf("%w: capability %q", ErrReplayed, nonce)
	}
	s.seen[nonce] = retainUntil
	return nil
}
