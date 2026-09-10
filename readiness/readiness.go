// Package readiness evaluates the readiness predicates declared on Codefly
// endpoints and dependencies.
//
// The declarations live in resources; this is the one place that turns them
// into an answer. Orchestrators and deployment renderers share it so that
// "ready" means the same thing everywhere, instead of each caller re-deriving
// it and settling for whatever an open socket implies.
package readiness

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// DefaultTimeout bounds one probe attempt when the declaration sets none.
const DefaultTimeout = 2 * time.Second

// Target carries the lifecycle facts only the owning agent knows, plus where to
// reach the endpoint. Transport security is NOT here: it is declared on the
// endpoint and travels on the requirement, so a caller cannot forget it.
type Target struct {
	// Address is the resolved host:port for a network predicate.
	Address string
	// Started reports that the owning agent's process reached and holds a
	// started state. It answers the agent predicate.
	Started bool
	// Completed reports that a one-shot workload terminated successfully. It
	// answers the completion predicate.
	Completed bool
}

func attemptTimeout(probe *basev0.Probe) time.Duration {
	if declared := probe.GetTiming().GetTimeout().AsDuration(); declared > 0 {
		return declared
	}
	return DefaultTimeout
}

// threshold reads a declared consecutive-result threshold. Zero means one: a
// probe that declares nothing reaches a verdict on its first attempt.
func threshold(declared uint32) uint32 {
	if declared == 0 {
		return 1
	}
	return declared
}

// wait sleeps unless the context ends first, which it reports.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type outcome struct {
	requirement *resources.ReadinessRequirement
}

func (o outcome) pass() *basev0.ProbeResult {
	return o.requirement.Result(basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNSPECIFIED, "")
}

func (o outcome) fail(kind basev0.ProbeFailureKind, format string, args ...any) *basev0.ProbeResult {
	return o.requirement.Result(basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, kind, fmt.Sprintf(format, args...))
}

// attempt is one evaluation of a predicate against an already-prepared target.
type attempt func(context.Context) *basev0.ProbeResult

// Check evaluates one requirement and returns its typed result. It never
// returns an error: a failing predicate is data the caller reports, not an
// exceptional condition.
//
// The probe's declared timing is binding. Check waits out initial_delay, then
// attempts the predicate every period until it sees success_threshold
// consecutive passes or failure_threshold consecutive failures. A probe that
// declares no timing is a single attempt.
func Check(ctx context.Context, requirement *resources.ReadinessRequirement, target Target) *basev0.ProbeResult {
	out := outcome{requirement: requirement}

	run, release, invalid := prepare(requirement, target, out)
	if invalid != nil {
		return invalid
	}
	defer release()

	timing := requirement.Probe.GetTiming()
	if err := wait(ctx, timing.GetInitialDelay().AsDuration()); err != nil {
		return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT,
			"the probe schedule was cancelled during its initial delay: %v", err)
	}

	successes, failures := threshold(timing.GetSuccessThreshold()), threshold(timing.GetFailureThreshold())
	var consecutivePasses, consecutiveFailures uint32
	var lastFailure *basev0.ProbeResult
	for {
		result := run(ctx)
		if result.GetOutcome() == basev0.ProbeOutcome_PROBE_OUTCOME_PASSED {
			consecutiveFailures = 0
			consecutivePasses++
			if consecutivePasses >= successes {
				return result
			}
		} else {
			consecutivePasses = 0
			consecutiveFailures++
			lastFailure = result
			if consecutiveFailures >= failures {
				return result
			}
		}
		if err := wait(ctx, timing.GetPeriod().AsDuration()); err != nil {
			if lastFailure != nil {
				return lastFailure
			}
			return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT,
				"the probe passed %d of %d required consecutive attempts before the schedule was cancelled: %v",
				consecutivePasses, successes, err)
		}
	}
}

// prepare validates the target and builds whatever the predicate reuses across
// attempts: one gRPC connection, one HTTP client. It returns a non-nil result
// when the target cannot be probed at all.
func prepare(requirement *resources.ReadinessRequirement, target Target, out outcome) (attempt, func(), *basev0.ProbeResult) {
	noop := func() {}

	switch predicate := requirement.Probe.GetPredicate().(type) {
	case *basev0.Probe_Agent:
		return func(context.Context) *basev0.ProbeResult {
			if target.Started {
				return out.pass()
			}
			return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_LIFECYCLE, "the owning agent does not report a started service")
		}, noop, nil

	case *basev0.Probe_Completion:
		return func(context.Context) *basev0.ProbeResult {
			if target.Completed {
				return out.pass()
			}
			return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INCOMPLETE, "the workload has not completed successfully")
		}, noop, nil

	case *basev0.Probe_Transport:
		if target.Address == "" {
			return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "no address was resolved for this endpoint")
		}
		limit := attemptTimeout(requirement.Probe)
		return func(ctx context.Context) *basev0.ProbeResult {
			dialer := net.Dialer{Timeout: limit}
			conn, err := dialer.DialContext(ctx, "tcp", target.Address)
			if err != nil {
				return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", target.Address, err)
			}
			_ = conn.Close()
			return out.pass()
		}, noop, nil

	case *basev0.Probe_GrpcHealth:
		return prepareGrpcHealth(predicate.GrpcHealth, requirement, target, out)

	case *basev0.Probe_Http:
		return prepareHTTP(predicate.Http, requirement, target, out)

	default:
		return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "no predicate is declared")
	}
}

func prepareGrpcHealth(probe *basev0.GrpcHealthProbe, requirement *resources.ReadinessRequirement, target Target, out outcome) (attempt, func(), *basev0.ProbeResult) {
	noop := func() {}
	if target.Address == "" {
		return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "no address was resolved for this endpoint")
	}

	// A secured endpoint answers TLS only. Dialling it in plaintext produces a
	// transport error that looks exactly like a dead service, forever.
	transport := insecure.NewCredentials()
	if requirement.Secured {
		host, _, err := net.SplitHostPort(target.Address)
		if err != nil {
			return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "%s is not a host:port address: %v", target.Address, err)
		}
		transport = credentials.NewTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}

	// One connection is reused for every attempt in the schedule: a probe with a
	// short period would otherwise open and tear down an HTTP/2 connection per
	// tick, per endpoint.
	conn, err := grpc.NewClient(target.Address, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "%s cannot be dialled: %v", target.Address, err)
	}
	limit := attemptTimeout(requirement.Probe)

	return func(ctx context.Context) *basev0.ProbeResult {
			ctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()

			response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: probe.GetService()})
			if err != nil {
				switch status.Code(err) {
				case codes.Unimplemented:
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNIMPLEMENTED,
						"%s does not serve grpc.health.v1.Health", target.Address)
				case codes.DeadlineExceeded:
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT, "%s: %v", target.Address, err)
				default:
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", target.Address, err)
				}
			}
			if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_NOT_SERVING, "%s answered %s", target.Address, response.GetStatus())
			}
			return out.pass()
		}, func() {
			_ = conn.Close()
		}, nil
}

func prepareHTTP(probe *basev0.HttpProbe, requirement *resources.ReadinessRequirement, target Target, out outcome) (attempt, func(), *basev0.ProbeResult) {
	noop := func() {}
	if target.Address == "" {
		return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "no address was resolved for this endpoint")
	}

	scheme := "http"
	if requirement.Secured {
		scheme = "https"
	}
	endpoint := fmt.Sprintf("%s://%s%s", scheme, target.Address, probe.GetPath())
	if _, err := url.Parse(endpoint); err != nil {
		return nil, noop, out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "%s is not a usable URL: %v", endpoint, err)
	}

	client := &http.Client{
		// A probe asks about the resource it names. Following a redirect answers
		// about a different one — a /healthz that redirects to a login page on
		// another host would otherwise pass as healthy — and hides every 3xx from
		// the declared status predicate.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	limit := attemptTimeout(requirement.Probe)

	return func(ctx context.Context) *basev0.ProbeResult {
			ctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()

			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, "%s cannot be requested: %v", endpoint, err)
			}
			response, err := client.Do(request)
			if err != nil {
				if ctx.Err() != nil {
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT, "%s: %v", endpoint, err)
				}
				return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", endpoint, err)
			}
			defer func() { _ = response.Body.Close() }()

			if !resources.HTTPStatusAccepted(probe, response.StatusCode) {
				return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_STATUS, "%s answered %d", endpoint, response.StatusCode)
			}
			if expected := probe.GetBodyContains(); expected != "" {
				body, err := io.ReadAll(response.Body)
				if err != nil {
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_BODY, "%s: %v", endpoint, err)
				}
				if !strings.Contains(string(body), expected) {
					return out.fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_BODY, "%s body does not contain %q", endpoint, expected)
				}
			}
			return out.pass()
		}, func() {
			client.CloseIdleConnections()
		}, nil
}

// Evaluate checks every requirement and reports whether all of them hold. The
// report is stamped with the lifecycle generation it was taken in, so a caller
// cannot carry a ready verdict across a restart.
//
// Requirements are checked concurrently: each one honours its own declared
// schedule, so evaluating them in sequence would make one slow dependency's
// timeout the floor for the whole pass.
func Evaluate(ctx context.Context, requirements []*resources.ReadinessRequirement, generation uint64, resolve func(*resources.ReadinessRequirement) Target) *basev0.HealthReport {
	// resolve runs on the caller's goroutine: it is a caller-supplied closure,
	// usually reading a shared map of network mappings, and is not ours to call
	// concurrently.
	targets := make([]Target, len(requirements))
	for i, requirement := range requirements {
		targets[i] = resolve(requirement)
	}

	results := make([]*basev0.ProbeResult, len(requirements))
	var group sync.WaitGroup
	for i := range requirements {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			results[i] = Check(ctx, requirements[i], targets[i])
		}(i)
	}
	group.Wait()

	report := &basev0.HealthReport{Generation: generation, Ready: true}
	for _, result := range results {
		if result.GetOutcome() != basev0.ProbeOutcome_PROBE_OUTCOME_PASSED {
			report.Ready = false
		}
		report.Results = append(report.Results, result)
	}
	return report
}
