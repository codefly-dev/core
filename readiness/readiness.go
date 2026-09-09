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
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// DefaultTimeout bounds one probe attempt when the declaration sets none.
const DefaultTimeout = 2 * time.Second

// Target carries what a predicate needs beyond its own declaration: where to
// reach the endpoint, and the lifecycle facts only the owning agent knows.
type Target struct {
	// Address is the resolved host:port for a network predicate.
	Address string
	// Secured selects https for an HTTP predicate.
	Secured bool
	// Started reports that the owning agent's process reached and holds a
	// started state. It answers the agent predicate.
	Started bool
	// Completed reports that a one-shot workload terminated successfully. It
	// answers the completion predicate.
	Completed bool
}

func timeout(probe *basev0.Probe) time.Duration {
	if declared := probe.GetTiming().GetTimeout().AsDuration(); declared > 0 {
		return declared
	}
	return DefaultTimeout
}

// Check evaluates one requirement and returns its typed result. It never
// returns an error: a failing predicate is data the caller reports, not an
// exceptional condition.
func Check(ctx context.Context, requirement *resources.ReadinessRequirement, target Target) *basev0.ProbeResult {
	pass := func() *basev0.ProbeResult {
		return requirement.Result(basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNSPECIFIED, "")
	}
	fail := func(kind basev0.ProbeFailureKind, format string, args ...any) *basev0.ProbeResult {
		return requirement.Result(basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, kind, fmt.Sprintf(format, args...))
	}

	switch predicate := requirement.Probe.GetPredicate().(type) {
	case *basev0.Probe_Agent:
		if target.Started {
			return pass()
		}
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_LIFECYCLE, "the owning agent does not report a started service")
	case *basev0.Probe_Completion:
		if target.Completed {
			return pass()
		}
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INCOMPLETE, "the workload has not completed successfully")
	case *basev0.Probe_Transport:
		return checkTransport(ctx, target.Address, timeout(requirement.Probe), pass, fail)
	case *basev0.Probe_GrpcHealth:
		return checkGrpcHealth(ctx, predicate.GrpcHealth, target.Address, timeout(requirement.Probe), pass, fail)
	case *basev0.Probe_Http:
		return checkHTTP(ctx, predicate.Http, target, timeout(requirement.Probe), pass, fail)
	default:
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNSPECIFIED, "no predicate is declared")
	}
}

type passFunc func() *basev0.ProbeResult
type failFunc func(basev0.ProbeFailureKind, string, ...any) *basev0.ProbeResult

func checkTransport(ctx context.Context, address string, limit time.Duration, pass passFunc, fail failFunc) *basev0.ProbeResult {
	dialer := net.Dialer{Timeout: limit}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", address, err)
	}
	_ = conn.Close()
	return pass()
}

func checkGrpcHealth(ctx context.Context, probe *basev0.GrpcHealthProbe, address string, limit time.Duration, pass passFunc, fail failFunc) *basev0.ProbeResult {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", address, err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: probe.GetService()})
	if err != nil {
		switch status.Code(err) {
		case codes.Unimplemented:
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNIMPLEMENTED,
				"%s does not serve grpc.health.v1.Health", address)
		case codes.DeadlineExceeded:
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT, "%s: %v", address, err)
		default:
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", address, err)
		}
	}
	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_NOT_SERVING, "%s answered %s", address, response.GetStatus())
	}
	return pass()
}

func checkHTTP(ctx context.Context, probe *basev0.HttpProbe, target Target, limit time.Duration, pass passFunc, fail failFunc) *basev0.ProbeResult {
	scheme := "http"
	if target.Secured {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, target.Address, probe.GetPath())

	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", url, err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_TIMEOUT, "%s: %v", url, err)
		}
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "%s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	if !resources.HTTPStatusAccepted(probe, response.StatusCode) {
		return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_STATUS, "%s answered %d", url, response.StatusCode)
	}
	if expected := probe.GetBodyContains(); expected != "" {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_BODY, "%s: %v", url, err)
		}
		if !strings.Contains(string(body), expected) {
			return fail(basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_BODY, "%s body does not contain %q", url, expected)
		}
	}
	return pass()
}

// Evaluate checks every requirement and reports whether all of them hold. The
// report is stamped with the lifecycle generation it was taken in, so a caller
// cannot carry a ready verdict across a restart.
func Evaluate(ctx context.Context, requirements []*resources.ReadinessRequirement, generation uint64, resolve func(*resources.ReadinessRequirement) Target) *basev0.HealthReport {
	report := &basev0.HealthReport{Generation: generation, Ready: true}
	for _, requirement := range requirements {
		result := Check(ctx, requirement, resolve(requirement))
		if result.GetOutcome() != basev0.ProbeOutcome_PROBE_OUTCOME_PASSED {
			report.Ready = false
		}
		report.Results = append(report.Results, result)
	}
	return report
}
