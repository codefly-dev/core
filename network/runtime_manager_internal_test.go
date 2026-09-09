package network

import (
	"context"
	"errors"
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
func seedEveryPortAllocated(m *RuntimeManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port := 1; port <= 65535; port++ {
		m.allocatedPorts[uint16(port)] = "foreign-owner"
	}
}

func randomPortReservations(m *RuntimeManager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, owner := range m.allocatedPorts {
		if owner == randomPortOwner {
			count++
		}
	}
	return count
}

func TestAllocateTemporaryPortExhaustsCollisionBudget(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	seedEveryPortAllocated(m)

	start := time.Now()
	port, err := m.AllocateTemporaryPort(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a bounded allocation failure, got port %d", port)
	}
	if port != 0 {
		t.Fatalf("failed allocation must not report a port, got %d", port)
	}
	if !strings.Contains(err.Error(), "already reserved by this manager") {
		t.Fatalf("error must carry the last collision cause, got %q", err)
	}
	budget := time.Duration(temporaryPortAttempts) * (temporaryPortBackoff + 500*time.Millisecond)
	if elapsed > budget {
		t.Fatalf("allocation took %s, over its %s budget", elapsed, budget)
	}
	if leaked := randomPortReservations(m); leaked != 0 {
		t.Fatalf("failed allocation leaked %d reservations", leaked)
	}
}

func TestAllocateTemporaryPortCancelDuringCollisionRetry(t *testing.T) {
	m, err := NewRuntimeManager(context.Background(), nil)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	seedEveryPortAllocated(m)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(temporaryPortBackoff)
		cancel()
	}()

	start := time.Now()
	port, err := m.AllocateTemporaryPort(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (port %d)", err, port)
	}
	if port != 0 {
		t.Fatalf("cancelled allocation must not report a port, got %d", port)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s to be observed", elapsed)
	}
	if leaked := randomPortReservations(m); leaked != 0 {
		t.Fatalf("cancelled allocation leaked %d reservations", leaked)
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
	if leaked := randomPortReservations(m); leaked != 0 {
		t.Fatalf("cancelled allocation leaked %d reservations", leaked)
	}
}
