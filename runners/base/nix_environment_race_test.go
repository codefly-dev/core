package base

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/codefly-dev/core/resources"
)

func TestNixEnvironmentRuntimeSnapshotConcurrentWithOverrides(t *testing.T) {
	env := &NixEnvironment{materialized: map[string]string{"PATH": "/nix/store/bin"}}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				env.WithEnvironmentVariables(context.Background(), &resources.EnvironmentVariable{
					Key:   fmt.Sprintf("WORKER_%d_%d", worker, i),
					Value: "set",
				})
			}
		}()
	}
	for i := 0; i < 100; i++ {
		materialized, _ := env.runtimeSnapshot()
		materialized["PATH"] = "mutated snapshot"
	}
	wg.Wait()

	materialized, overrides := env.runtimeSnapshot()
	if materialized["PATH"] != "/nix/store/bin" {
		t.Fatalf("snapshot mutated environment state: %q", materialized["PATH"])
	}
	if len(overrides) != 400 {
		t.Fatalf("override count = %d, want 400", len(overrides))
	}
}
