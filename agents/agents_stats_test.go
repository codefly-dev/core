package agents

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRecordRPCDuration_AccumulatesCorrectly(t *testing.T) {
	ResetRPCStats()

	recordRPCDuration("Test", 10*time.Millisecond, nil)
	recordRPCDuration("Test", 50*time.Millisecond, nil)
	recordRPCDuration("Test", 200*time.Millisecond, errors.New("boom"))

	snap := SnapshotRPCStats()
	s, ok := snap["Test"]
	if !ok {
		t.Fatal("Test not present in snapshot")
	}
	if s.Count != 3 {
		t.Errorf("Count = %d, want 3", s.Count)
	}
	if s.Errors != 1 {
		t.Errorf("Errors = %d, want 1", s.Errors)
	}
	if s.Sum != 260*time.Millisecond {
		t.Errorf("Sum = %v, want 260ms", s.Sum)
	}
	if s.Max != 200*time.Millisecond {
		t.Errorf("Max = %v, want 200ms", s.Max)
	}
}

// TestRecordRPCDuration_BucketBoundaries asserts each call lands in the
// expected histogram bucket. Boundaries are upper-inclusive.
func TestRecordRPCDuration_BucketBoundaries(t *testing.T) {
	ResetRPCStats()

	cases := []struct {
		dur  time.Duration
		want int // bucket index expected to increment
	}{
		{500 * time.Microsecond, 0}, // <= 1ms
		{1 * time.Millisecond, 0},   // exactly 1ms — upper-inclusive
		{3 * time.Millisecond, 1},   // (1ms, 5ms]
		{20 * time.Millisecond, 2},  // (5ms, 25ms]
		{80 * time.Millisecond, 3},  // (25ms, 100ms]
		{300 * time.Millisecond, 4}, // (100ms, 500ms]
		{800 * time.Millisecond, 5}, // (500ms, 1s]
		{3 * time.Second, 6},        // (1s, 5s]
		{20 * time.Second, 7},       // (5s, 30s]
		{2 * time.Minute, 8},        // overflow
	}

	for _, tc := range cases {
		recordRPCDuration("Probe", tc.dur, nil)
	}

	snap := SnapshotRPCStats()
	s := snap["Probe"]
	for i, c := range tc(cases) {
		if s.Buckets[i] != c {
			t.Errorf("bucket[%d] = %d, want %d", i, s.Buckets[i], c)
		}
	}
}

// TestSnapshotRPCStats_IsDeepCopy ensures callers can't mutate the live
// map by writing to the snapshot.
func TestSnapshotRPCStats_IsDeepCopy(t *testing.T) {
	ResetRPCStats()
	recordRPCDuration("Test", 10*time.Millisecond, nil)

	snap := SnapshotRPCStats()
	s := snap["Test"]
	s.Count = 999 // mutate the snapshot copy

	snap2 := SnapshotRPCStats()
	if snap2["Test"].Count != 1 {
		t.Errorf("snapshot mutation leaked back to live stats: count=%d", snap2["Test"].Count)
	}
}

// TestRecordRPCDuration_Concurrency proves the mutex serializes
// concurrent recordings without lost updates.
func TestRecordRPCDuration_Concurrency(t *testing.T) {
	ResetRPCStats()

	var wg sync.WaitGroup
	const goroutines = 20
	const callsEach = 100
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsEach; j++ {
				recordRPCDuration("Hot", 1*time.Millisecond, nil)
			}
		}()
	}
	wg.Wait()

	snap := SnapshotRPCStats()
	want := uint64(goroutines * callsEach)
	if snap["Hot"].Count != want {
		t.Errorf("Count = %d, want %d (lost updates indicate race)", snap["Hot"].Count, want)
	}
}

// tc is a tiny one-liner that converts the test cases into the parallel
// expected-bucket-counts slice. Keeps the assertion above readable.
func tc(in []struct {
	dur  time.Duration
	want int
}) [9]uint64 {
	var out [9]uint64
	for _, c := range in {
		out[c.want]++
	}
	return out
}
