package resources

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/standards"
)

// ProbeKind is the YAML spelling of a health predicate family.
type ProbeKind = string

const (
	// ProbeKindTransport only connects to the endpoint address.
	ProbeKindTransport ProbeKind = "transport"
	// ProbeKindGRPCHealth queries grpc.health.v1 and requires SERVING.
	ProbeKindGRPCHealth ProbeKind = "grpc-health"
	// ProbeKindHTTP issues an HTTP GET and matches status and body.
	ProbeKindHTTP ProbeKind = "http"
	// ProbeKindAgent defers to the owning runtime agent's reported health.
	ProbeKindAgent ProbeKind = "agent"
	// ProbeKindCompletion requires a one-shot workload to have finished.
	ProbeKindCompletion ProbeKind = "completion"
)

// ProbeKinds are every health predicate family.
func ProbeKinds() []ProbeKind {
	return []ProbeKind{ProbeKindTransport, ProbeKindGRPCHealth, ProbeKindHTTP, ProbeKindAgent, ProbeKindCompletion}
}

// EndpointProbeKinds are the families an endpoint may declare. Completion is
// excluded: it describes a workload that terminates, which a listening endpoint
// never does. Error messages offer this list so they cannot recommend a kind
// that the next validation step rejects.
func EndpointProbeKinds() []ProbeKind {
	return []ProbeKind{ProbeKindTransport, ProbeKindGRPCHealth, ProbeKindHTTP, ProbeKindAgent}
}

// DependencyReadiness is what "ready" means for a dependency that exposes no
// endpoint to probe.
type DependencyReadiness = string

const (
	// DependencyReadinessStarted only requires the dependency's runtime agent to
	// have reached a started state. It is the legacy behavior and the default
	// for a service dependency.
	DependencyReadinessStarted DependencyReadiness = "started"
	// DependencyReadinessCompleted requires a one-shot workload to have
	// terminated successfully. It is the default for a job dependency: a
	// migration that is still running has not prepared anything.
	DependencyReadinessCompleted DependencyReadiness = "completed"
	// DependencyReadinessIgnore contributes no requirement at all. It is how a
	// consumer says it reaches a dependency opportunistically: the endpoints are
	// still resolved and mapped, they just do not gate startup.
	DependencyReadinessIgnore DependencyReadiness = "ignore"
)

// DependencyReadinessModes are the values a dependency readiness field accepts.
func DependencyReadinessModes() []DependencyReadiness {
	return []DependencyReadiness{DependencyReadinessStarted, DependencyReadinessCompleted, DependencyReadinessIgnore}
}

// Probe is the authored form of one health predicate. Kind selects which of the
// remaining fields apply; declaring a field that belongs to another kind is an
// error rather than a silently ignored key.
type Probe struct {
	Kind ProbeKind `yaml:"kind"`

	// Service is the grpc.health.v1 service name to query. Empty is the
	// conventional whole-server check.
	Service string `yaml:"service,omitempty"`

	// Path is the HTTP request path. It has no default: a service that never
	// served a health path must not be asked for one.
	Path string `yaml:"path,omitempty"`
	// Statuses are accepted HTTP status codes, each "200" or "200-299". Empty
	// accepts 200-399.
	Statuses []string `yaml:"statuses,omitempty"`
	// BodyContains, when set, requires the HTTP response body to contain it.
	BodyContains string `yaml:"body-contains,omitempty"`

	InitialDelay     string `yaml:"initial-delay,omitempty"`
	Period           string `yaml:"period,omitempty"`
	Timeout          string `yaml:"timeout,omitempty"`
	FailureThreshold uint32 `yaml:"failure-threshold,omitempty"`
	SuccessThreshold uint32 `yaml:"success-threshold,omitempty"`
}

// Health declares the predicates for one endpoint. The three intents are
// independent: readiness gates consumers, liveness decides restarts, startup
// gates the boot window.
type Health struct {
	Readiness *Probe `yaml:"readiness,omitempty"`
	Liveness  *Probe `yaml:"liveness,omitempty"`
	Startup   *Probe `yaml:"startup,omitempty"`
}

// defaultHTTPStatuses is what an HTTP probe accepts when it declares nothing:
// success and redirects, but never a 4xx or 5xx. It is built per call because
// it is handed to callers, who must not be able to edit the default for every
// probe in the process.
func defaultHTTPStatuses() []*basev0.HttpStatusRange {
	return []*basev0.HttpStatusRange{{Min: 200, Max: 399}}
}

func parseStatusRange(raw string) (*basev0.HttpStatusRange, error) {
	bounds := strings.SplitN(strings.TrimSpace(raw), "-", 2)
	codes := make([]uint32, 0, 2)
	for _, bound := range bounds {
		code, err := strconv.ParseUint(strings.TrimSpace(bound), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("status %q is not a number or a range such as \"200-299\"", raw)
		}
		if code < 100 || code > 599 {
			return nil, fmt.Errorf("status %q is outside the HTTP status range 100-599", raw)
		}
		codes = append(codes, uint32(code))
	}
	if len(codes) == 1 {
		return &basev0.HttpStatusRange{Min: codes[0], Max: codes[0]}, nil
	}
	if codes[0] > codes[1] {
		return nil, fmt.Errorf("status range %q is inverted", raw)
	}
	return &basev0.HttpStatusRange{Min: codes[0], Max: codes[1]}, nil
}

func parseProbeDuration(field, raw string) (*durationpb.Duration, error) {
	if raw == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %q is not a duration such as \"5s\"", field, raw)
	}
	if d < 0 {
		return nil, fmt.Errorf("%s %q must not be negative", field, raw)
	}
	return durationpb.New(d), nil
}

func (probe *Probe) timing() (*basev0.ProbeTiming, error) {
	initialDelay, err := parseProbeDuration("initial-delay", probe.InitialDelay)
	if err != nil {
		return nil, err
	}
	period, err := parseProbeDuration("period", probe.Period)
	if err != nil {
		return nil, err
	}
	timeout, err := parseProbeDuration("timeout", probe.Timeout)
	if err != nil {
		return nil, err
	}
	if initialDelay == nil && period == nil && timeout == nil && probe.FailureThreshold == 0 && probe.SuccessThreshold == 0 {
		return nil, nil
	}
	return &basev0.ProbeTiming{
		InitialDelay:     initialDelay,
		Period:           period,
		Timeout:          timeout,
		FailureThreshold: probe.FailureThreshold,
		SuccessThreshold: probe.SuccessThreshold,
	}, nil
}

// misplacedFields names the fields that were set but do not belong to kind, so
// a contradictory declaration fails instead of quietly dropping half of itself.
func (probe *Probe) misplacedFields() []string {
	set := map[string]bool{
		"service":       probe.Service != "",
		"path":          probe.Path != "",
		"statuses":      len(probe.Statuses) > 0,
		"body-contains": probe.BodyContains != "",
	}
	var belongs []string
	switch probe.Kind {
	case ProbeKindGRPCHealth:
		belongs = []string{"service"}
	case ProbeKindHTTP:
		belongs = []string{"path", "statuses", "body-contains"}
	}
	var misplaced []string
	for _, field := range []string{"service", "path", "statuses", "body-contains"} {
		if set[field] && !slices.Contains(belongs, field) {
			misplaced = append(misplaced, field)
		}
	}
	return misplaced
}

// Proto converts a declared probe into its wire form, validating it against the
// API of the endpoint that carries it.
func (probe *Probe) Proto(api string) (*basev0.Probe, error) {
	if probe == nil {
		return nil, nil
	}
	if probe.Kind == "" {
		return nil, fmt.Errorf("probe needs a kind (one of %s)", strings.Join(EndpointProbeKinds(), ", "))
	}
	if !slices.Contains(ProbeKinds(), probe.Kind) {
		return nil, fmt.Errorf("unsupported probe kind %q (expected one of %s)", probe.Kind, strings.Join(EndpointProbeKinds(), ", "))
	}
	if misplaced := probe.misplacedFields(); len(misplaced) > 0 {
		return nil, fmt.Errorf("probe kind %q does not accept %s", probe.Kind, strings.Join(misplaced, ", "))
	}
	timing, err := probe.timing()
	if err != nil {
		return nil, err
	}
	out := &basev0.Probe{Timing: timing}
	switch probe.Kind {
	case ProbeKindTransport:
		out.Predicate = &basev0.Probe_Transport{Transport: &basev0.TransportProbe{}}
	case ProbeKindAgent:
		out.Predicate = &basev0.Probe_Agent{Agent: &basev0.AgentProbe{}}
	case ProbeKindCompletion:
		return nil, fmt.Errorf("probe kind %q is only valid for a dependency that exposes no endpoint, not for an %s endpoint", probe.Kind, api)
	case ProbeKindGRPCHealth:
		if api != standards.GRPC {
			return nil, fmt.Errorf("probe kind %q requires a %s endpoint, not %s", probe.Kind, standards.GRPC, api)
		}
		out.Predicate = &basev0.Probe_GrpcHealth{GrpcHealth: &basev0.GrpcHealthProbe{Service: probe.Service}}
	case ProbeKindHTTP:
		if !standards.IsHTTPBasedAPI(api) {
			return nil, fmt.Errorf("probe kind %q requires an HTTP-based endpoint, not %s", probe.Kind, api)
		}
		if probe.Path == "" {
			return nil, fmt.Errorf("probe kind %q needs a path; there is no default health path", probe.Kind)
		}
		if !strings.HasPrefix(probe.Path, "/") {
			return nil, fmt.Errorf("probe path %q must start with %q", probe.Path, "/")
		}
		http := &basev0.HttpProbe{Path: probe.Path, BodyContains: probe.BodyContains}
		for _, raw := range probe.Statuses {
			status, err := parseStatusRange(raw)
			if err != nil {
				return nil, err
			}
			http.Statuses = append(http.Statuses, status)
		}
		out.Predicate = &basev0.Probe_Http{Http: http}
	}
	return out, nil
}

func probeDurationString(d *durationpb.Duration) string {
	if d == nil {
		return ""
	}
	return d.AsDuration().String()
}

// ProbeFromProto converts a wire probe back to its authored form.
func ProbeFromProto(probe *basev0.Probe) *Probe {
	if probe == nil || probe.GetPredicate() == nil {
		return nil
	}
	out := &Probe{
		InitialDelay:     probeDurationString(probe.GetTiming().GetInitialDelay()),
		Period:           probeDurationString(probe.GetTiming().GetPeriod()),
		Timeout:          probeDurationString(probe.GetTiming().GetTimeout()),
		FailureThreshold: probe.GetTiming().GetFailureThreshold(),
		SuccessThreshold: probe.GetTiming().GetSuccessThreshold(),
	}
	switch predicate := probe.GetPredicate().(type) {
	case *basev0.Probe_Transport:
		out.Kind = ProbeKindTransport
	case *basev0.Probe_Agent:
		out.Kind = ProbeKindAgent
	case *basev0.Probe_Completion:
		out.Kind = ProbeKindCompletion
	case *basev0.Probe_GrpcHealth:
		out.Kind = ProbeKindGRPCHealth
		out.Service = predicate.GrpcHealth.GetService()
	case *basev0.Probe_Http:
		out.Kind = ProbeKindHTTP
		out.Path = predicate.Http.GetPath()
		out.BodyContains = predicate.Http.GetBodyContains()
		for _, status := range predicate.Http.GetStatuses() {
			if status.GetMin() == status.GetMax() {
				out.Statuses = append(out.Statuses, strconv.FormatUint(uint64(status.GetMin()), 10))
				continue
			}
			out.Statuses = append(out.Statuses, fmt.Sprintf("%d-%d", status.GetMin(), status.GetMax()))
		}
	}
	return out
}

// Proto converts a declared health block into its wire form, naming the intent
// whose probe is invalid.
func (health *Health) Proto(api string) (*basev0.Health, error) {
	if health == nil {
		return nil, nil
	}
	out := &basev0.Health{}
	for _, intent := range []struct {
		name  string
		probe *Probe
		set   func(*basev0.Probe)
	}{
		{"readiness", health.Readiness, func(p *basev0.Probe) { out.Readiness = p }},
		{"liveness", health.Liveness, func(p *basev0.Probe) { out.Liveness = p }},
		{"startup", health.Startup, func(p *basev0.Probe) { out.Startup = p }},
	} {
		probe, err := intent.probe.Proto(api)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", intent.name, err)
		}
		intent.set(probe)
	}
	if out.Readiness == nil && out.Liveness == nil && out.Startup == nil {
		return nil, nil
	}
	return out, nil
}

// HealthFromProto converts a wire health block back to its authored form.
func HealthFromProto(health *basev0.Health) *Health {
	if health == nil {
		return nil
	}
	out := &Health{
		Readiness: ProbeFromProto(health.GetReadiness()),
		Liveness:  ProbeFromProto(health.GetLiveness()),
		Startup:   ProbeFromProto(health.GetStartup()),
	}
	if out.Readiness == nil && out.Liveness == nil && out.Startup == nil {
		return nil
	}
	return out
}

// validateEndpointHealth reports the first invalid health declaration, prefixed
// with the endpoint it came from so the error points at the source.
func validateEndpointHealth(endpoints []*Endpoint) error {
	for _, endpoint := range endpoints {
		if endpoint.Health == nil {
			continue
		}
		api := endpoint.API
		if api == "" && slices.Contains(standards.APIS(), endpoint.Name) {
			api = endpoint.Name
		}
		if _, err := endpoint.Health.Proto(api); err != nil {
			return fmt.Errorf("endpoint %q health: %w", endpoint.Name, err)
		}
	}
	return nil
}

// ValidateEndpointHealth reports whether a health block that arrived over the
// wire is usable, applying the same rules as an authored declaration. An agent
// can return any Endpoint it likes from Load; without this a contradiction such
// as a gRPC health probe on an HTTP endpoint is accepted at ingestion and only
// fails much later, when the service manifest is written back to disk.
func ValidateEndpointHealth(endpoint *basev0.Endpoint) error {
	health := endpoint.GetHealth()
	if health == nil {
		return nil
	}
	for _, intent := range []struct {
		name  string
		probe *basev0.Probe
	}{
		{"readiness", health.GetReadiness()},
		{"liveness", health.GetLiveness()},
		{"startup", health.GetStartup()},
	} {
		if intent.probe == nil {
			continue
		}
		if intent.probe.GetPredicate() == nil {
			return fmt.Errorf("endpoint %q health: %s declares no predicate", endpoint.GetName(), intent.name)
		}
		if _, err := ProbeFromProto(intent.probe).Proto(endpoint.GetApi()); err != nil {
			return fmt.Errorf("endpoint %q health: %s: %w", endpoint.GetName(), intent.name, err)
		}
	}
	return nil
}

func validateDependencyReadinessMode(unique string, readiness DependencyReadiness) error {
	if readiness == "" || slices.Contains(DependencyReadinessModes(), readiness) {
		return nil
	}
	return fmt.Errorf("dependency %s declares unsupported readiness %q (expected one of %s)",
		unique, readiness, strings.Join(DependencyReadinessModes(), ", "))
}

// ProbeKindOf labels a wire probe with its predicate family.
func ProbeKindOf(probe *basev0.Probe) basev0.ProbeKind {
	switch probe.GetPredicate().(type) {
	case *basev0.Probe_Transport:
		return basev0.ProbeKind_PROBE_KIND_TRANSPORT
	case *basev0.Probe_GrpcHealth:
		return basev0.ProbeKind_PROBE_KIND_GRPC_HEALTH
	case *basev0.Probe_Http:
		return basev0.ProbeKind_PROBE_KIND_HTTP
	case *basev0.Probe_Agent:
		return basev0.ProbeKind_PROBE_KIND_AGENT
	case *basev0.Probe_Completion:
		return basev0.ProbeKind_PROBE_KIND_COMPLETION
	default:
		return basev0.ProbeKind_PROBE_KIND_UNSPECIFIED
	}
}

// DescribeProbe renders the predicate a probe requires, for the message a
// consumer shows when readiness does not hold.
func DescribeProbe(probe *basev0.Probe) string {
	switch predicate := probe.GetPredicate().(type) {
	case *basev0.Probe_Transport:
		return "transport connect"
	case *basev0.Probe_Agent:
		return "agent-reported readiness"
	case *basev0.Probe_Completion:
		return "successful completion"
	case *basev0.Probe_GrpcHealth:
		if service := predicate.GrpcHealth.GetService(); service != "" {
			return fmt.Sprintf("grpc health SERVING for %q", service)
		}
		return "grpc health SERVING"
	case *basev0.Probe_Http:
		statuses := AcceptedHTTPStatuses(predicate.Http)
		rendered := make([]string, 0, len(statuses))
		for _, status := range statuses {
			if status.GetMin() == status.GetMax() {
				rendered = append(rendered, strconv.FormatUint(uint64(status.GetMin()), 10))
				continue
			}
			rendered = append(rendered, fmt.Sprintf("%d-%d", status.GetMin(), status.GetMax()))
		}
		description := fmt.Sprintf("http GET %s status in %s", predicate.Http.GetPath(), strings.Join(rendered, ","))
		if body := predicate.Http.GetBodyContains(); body != "" {
			description += fmt.Sprintf(" and body containing %q", body)
		}
		return description
	default:
		return "no predicate"
	}
}

// AcceptedHTTPStatuses returns the status ranges an HTTP probe accepts, with
// the default applied. A probe that declares none still rejects 4xx and 5xx —
// the point of the audit finding, where any reachable HTTP server passed.
func AcceptedHTTPStatuses(probe *basev0.HttpProbe) []*basev0.HttpStatusRange {
	if len(probe.GetStatuses()) == 0 {
		return defaultHTTPStatuses()
	}
	return probe.GetStatuses()
}

// HTTPStatusAccepted reports whether status satisfies the probe's predicate.
func HTTPStatusAccepted(probe *basev0.HttpProbe, status int) bool {
	for _, accepted := range AcceptedHTTPStatuses(probe) {
		if uint32(status) >= accepted.GetMin() && uint32(status) <= accepted.GetMax() {
			return true
		}
	}
	return false
}
