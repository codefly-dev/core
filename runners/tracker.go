package runners

import (
	"context"
	"time"

	observabilityv1 "github.com/codefly-dev/core/generated/go/observability/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
)

func Track(ctx context.Context, trackers []*runtimev1.Tracker) (chan Event, error) {
	events := make(chan Event)
	var trackeds []Tracked
	for _, t := range trackers {
		tracked, err := NewTracked(t)
		if err != nil {
			return nil, err
		}
		trackeds = append(trackeds, tracked)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(1000 * time.Millisecond)
				for _, tracked := range trackeds {
					state, err := tracked.GetState(ctx)
					if err != nil {
						continue
					}
					events <- Event{
						ProcessState: state,
					}
					cpu, err := tracked.GetCPU(ctx)
					if err != nil {
						continue
					}
					events <- Event{
						CPU: &observabilityv1.CPU{Usage: cpu.usage},
					}
					memory, err := tracked.GetMemory(ctx)
					if err != nil {
						continue
					}
					events <- Event{
						Memory: &observabilityv1.Memory{Usage: float64(memory.used)},
					}
				}

			}
		}
	}()
	return events, nil
}
