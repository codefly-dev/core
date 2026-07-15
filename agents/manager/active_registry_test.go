package manager

import "testing"

func resetActiveRegistryForTest() {
	activeMu.Lock()
	active = make(map[string]map[*AgentConn]struct{})
	activeMu.Unlock()
}

func TestActiveRegistryTracksEverySpawnForIdentity(t *testing.T) {
	resetActiveRegistryForTest()
	defer resetActiveRegistryForTest()

	first := &AgentConn{}
	second := &AgentConn{}
	registerActive("codefly.dev/go-grpc", first)
	registerActive("codefly.dev/go-grpc", second)

	activeMu.Lock()
	count := len(active["codefly.dev/go-grpc"])
	activeMu.Unlock()
	if count != 2 {
		t.Fatalf("active spawn count = %d, want 2", count)
	}

	first.Close()
	activeMu.Lock()
	count = len(active["codefly.dev/go-grpc"])
	activeMu.Unlock()
	if count != 1 {
		t.Fatalf("closing one spawn left %d entries, want 1", count)
	}

	Cleanup("codefly.dev/go-grpc")
	activeMu.Lock()
	_, present := active["codefly.dev/go-grpc"]
	activeMu.Unlock()
	if present {
		t.Fatal("Cleanup left active connections for the identity")
	}
}

func TestAgentConnCloseIsIdempotent(t *testing.T) {
	resetActiveRegistryForTest()
	defer resetActiveRegistryForTest()
	conn := &AgentConn{}
	registerActive("agent", conn)
	conn.Close()
	conn.Close()
}
