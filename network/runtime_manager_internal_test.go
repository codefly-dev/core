package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKernelTemporaryPortProbeOwnsLoopbackPort(t *testing.T) {
	listener, allocatedPort, err := listenTemporaryPort()
	if err != nil {
		t.Fatalf("bind kernel-assigned loopback port: %v", err)
	}
	defer listener.Close()

	port := int(allocatedPort)
	if port == 0 {
		t.Fatal("kernel returned port zero")
	}

	second, err := net.Listen("tcp4", net.JoinHostPort(Localhost, strconv.Itoa(port)))
	if err == nil {
		second.Close()
		t.Fatalf("kernel-assigned port %d was not exclusively reserved by the probe listener", port)
	}
}

// seedEveryPortAllocated makes every kernel offer a reservation collision, so
// AllocateTemporaryPort's retry path runs deterministically without waiting on
// the kernel to actually re-hand a port.
func seedEveryPortAllocated(m *RuntimeManager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port := 1; port <= 65535; port++ {
		m.allocatedPorts[uint16(port)] = "foreign-owner"
	}
	return len(m.allocatedPorts)
}

func reservationCount(m *RuntimeManager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.allocatedPorts)
}

func TestAllocateTemporaryPortExhaustsCollisionBudget(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	seeded := seedEveryPortAllocated(m)

	start := time.Now()
	port, err := m.AllocateTemporaryPort(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a bounded allocation failure, got port %d", port)
	}
	if port != 0 {
		t.Fatalf("failed allocation must not report a port, got %d", port)
	}
	if !errors.Is(err, ErrTemporaryPortUnavailable) {
		t.Fatalf("a manager with no free port must report ErrTemporaryPortUnavailable, got %v", err)
	}
	if errors.Is(err, ErrTemporaryPortUnsupported) {
		t.Fatalf("a full manager is not an unsupported host: %v", err)
	}
	if !strings.Contains(err.Error(), "already reserved by this manager") {
		t.Fatalf("error must carry the last collision cause, got %q", err)
	}
	// The loop is 16 attempts of (bind, close, 5ms backoff): ~110ms measured.
	// Keep the bound close enough to that to actually catch a regression —
	// the whole point of the budget is that it is small.
	budget := temporaryPortAttempts*temporaryPortBackoff + time.Second
	if elapsed > budget {
		t.Fatalf("allocation took %s, over its %s budget", elapsed, budget)
	}
	// The collision path must leave the map exactly as it found it. Counting
	// entries (rather than looking for randomPortOwner, which this seeding
	// makes unreachable) is what fails if a future change records a
	// reservation it does not roll back.
	if got := reservationCount(m); got != seeded {
		t.Fatalf("failed allocation changed the reservation count: %d -> %d", seeded, got)
	}
}

func TestAllocateTemporaryPortCancelDuringCollisionRetry(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	seeded := seedEveryPortAllocated(m)

	// Cancel far enough into the ~110ms retry loop that a descheduled
	// goroutine cannot let the budget finish first, and far enough from the
	// start that the cancellation lands mid-retry rather than before attempt 1.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	port, err := m.AllocateTemporaryPort(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (port %d)", err, port)
	}
	if errors.Is(err, ErrTemporaryPortUnavailable) {
		t.Fatalf("cancellation must not be reported as an exhausted budget: %v", err)
	}
	if port != 0 {
		t.Fatalf("cancelled allocation must not report a port, got %d", port)
	}
	// Cancellation must stop the loop, not merely be noticed at the end of it:
	// the error names how many attempts ran, and it must be short of the budget.
	if strings.Contains(err.Error(), fmt.Sprintf("%d/%d attempts", temporaryPortAttempts, temporaryPortAttempts)) {
		t.Fatalf("cancellation was only observed after the full budget ran: %v", err)
	}
	if got := reservationCount(m); got != seeded {
		t.Fatalf("cancelled allocation changed the reservation count: %d -> %d", seeded, got)
	}
}

func TestAllocateTemporaryPortCancelledBeforeAllocation(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port, err := m.AllocateTemporaryPort(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (port %d)", err, port)
	}
	if port != 0 {
		t.Fatalf("cancelled allocation must not report a port, got %d", port)
	}
	// Nothing was bound, so nothing may be reserved: ports are freely
	// available here, and only the cancellation check stops the allocation.
	if got := reservationCount(m); got != 0 {
		t.Fatalf("cancelled allocation reserved %d ports", got)
	}
}

func TestAllocateTemporaryPortExpiredDeadline(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	port, err := m.AllocateTemporaryPort(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded to survive wrapping, got %v (port %d)", err, port)
	}
	if got := reservationCount(m); got != 0 {
		t.Fatalf("expired-deadline allocation reserved %d ports", got)
	}
}

// TestReleasePortDropsReservation covers the rollback AllocateTemporaryPort
// performs when closing its probe listener fails — a path no test can trigger
// through net.TCPListener.Close, but whose effect is exactly this call.
func TestReleasePortDropsReservation(t *testing.T) {
	ctx := context.Background()
	m, err := NewRuntimeManager(ctx, nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	port, err := m.AllocateTemporaryPort(ctx)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if got := reservationCount(m); got != 1 {
		t.Fatalf("expected the allocated port to be reserved, count is %d", got)
	}

	m.ReleasePort(port)

	if got := reservationCount(m); got != 0 {
		t.Fatalf("released port left %d reservations behind", got)
	}
	m.mu.Lock()
	_, stillHeld := m.allocatedPorts[port]
	m.mu.Unlock()
	if stillHeld {
		t.Fatalf("port %d is still reserved after release", port)
	}
}
