package policy

import (
	"fmt"
	"testing"
	"time"
)

func TestRateLimitStateIsBoundedAndFailsClosed(t *testing.T) {
	state := newRateLimitState()
	now := time.Now()
	for index := 0; index < maxRateLimitKeys; index++ {
		if err := state.check(fmt.Sprintf("principal-%d", index), 1, now); err != nil {
			t.Fatalf("populate key %d: %v", index, err)
		}
	}
	if err := state.check("one-too-many", 1, now); err == nil {
		t.Fatal("expected a new key at capacity to fail closed")
	}
	if len(state.windows) != maxRateLimitKeys {
		t.Fatalf("state grew to %d entries, want %d", len(state.windows), maxRateLimitKeys)
	}
}

func TestRateLimitStatePrunesExpiredKeysAtCapacity(t *testing.T) {
	state := newRateLimitState()
	old := time.Now().Add(-2 * time.Minute)
	for index := 0; index < maxRateLimitKeys; index++ {
		state.windows[fmt.Sprintf("expired-%d", index)] = []time.Time{old}
	}

	if err := state.check("fresh", 1, time.Now()); err != nil {
		t.Fatalf("stale entries should be reclaimed: %v", err)
	}
	if len(state.windows) != 1 {
		t.Fatalf("state retained %d entries after stale sweep", len(state.windows))
	}
}
