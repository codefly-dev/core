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

	mu   sync.Mutex
	seen map[string]time.Time
}

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
	for spent, until := range s.seen {
		if until.Before(now) {
			delete(s.seen, spent)
		}
	}
	if _, spent := s.seen[nonce]; spent {
		return fmt.Errorf("%w: capability %q", ErrReplayed, nonce)
	}
	s.seen[nonce] = retainUntil
	return nil
}
