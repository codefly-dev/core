package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// Runnable declaration constants.
const (
	RunnableConfigurationName = "runnable.codefly.yaml"
	RunnableKind              = "runnable"

	// RunnableProtocolV1 is the invocation protocol between a launcher and a
	// generated harness: bounded JSON in, bounded JSON out, logs kept apart from
	// completion data.
	RunnableProtocolV1 = "codefly.runnable/v1"

	// DefaultRunnablePayloadBytes bounds an inline invocation payload when the
	// declaration does not. Larger data travels as owner-authorized references.
	DefaultRunnablePayloadBytes uint64 = 1 << 20
)

// RunnableProtocols are the invocation protocols this core understands. A
// declaration naming any other protocol is rejected rather than best-effort
// parsed: the harness a language agent generates and the launcher the CLI runs
// must agree on the same seam.
func RunnableProtocols() []string {
	return []string{RunnableProtocolV1}
}

// RunnableFieldType is the YAML spelling of one bounded-profile value type.
type RunnableFieldType string

// The bounded profile's value types.
const (
	RunnableFieldString  RunnableFieldType = "string"
	RunnableFieldInteger RunnableFieldType = "integer"
	RunnableFieldBoolean RunnableFieldType = "boolean"
	RunnableFieldObject  RunnableFieldType = "object"
	RunnableFieldArray   RunnableFieldType = "array"
)

// RunnableFieldTypes are every value type of the bounded profile.
func RunnableFieldTypes() []RunnableFieldType {
	return []RunnableFieldType{RunnableFieldString, RunnableFieldInteger, RunnableFieldBoolean, RunnableFieldObject, RunnableFieldArray}
}

var runnableFieldTypeProto = map[RunnableFieldType]basev0.RunnableField_Type{
	RunnableFieldString:  basev0.RunnableField_STRING,
	RunnableFieldInteger: basev0.RunnableField_INTEGER,
	RunnableFieldBoolean: basev0.RunnableField_BOOLEAN,
	RunnableFieldObject:  basev0.RunnableField_OBJECT,
	RunnableFieldArray:   basev0.RunnableField_ARRAY,
}

// runnableFieldNamePattern keeps field names representable as identifiers in
// every language a runnable agent generates bindings for.
var runnableFieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RunnableField is one field of an invocation payload. Optional means the key
// may be absent; nullable means the value may be null. They are independent.
type RunnableField struct {
	Name        string            `yaml:"name,omitempty"`
	Type        RunnableFieldType `yaml:"type"`
	Description string            `yaml:"description,omitempty"`
	Optional    bool              `yaml:"optional,omitempty"`
	Nullable    bool              `yaml:"nullable,omitempty"`
	Fields      []*RunnableField  `yaml:"fields,omitempty"`
	Items       *RunnableField    `yaml:"items,omitempty"`
}

// RunnableSchema is the shape of one invocation payload; a payload is always
// an object.
type RunnableSchema struct {
	Fields []*RunnableField `yaml:"fields"`
}

// RunnableContract is the typed finite operation a runnable implements.
type RunnableContract struct {
	Protocol string          `yaml:"protocol"`
	Input    *RunnableSchema `yaml:"input"`
	Output   *RunnableSchema `yaml:"output"`
}

// RunnableEntrypoint identifies the author entrypoint and the build inputs
// whose content changes the package.
type RunnableEntrypoint struct {
	Handler string   `yaml:"handler"`
	Inputs  []string `yaml:"inputs,omitempty"`
}

// RunnableFacility is the YAML spelling of an execution facility.
type RunnableFacility string

// Execution facilities a runnable may be bound to.
const (
	RunnableFacilityNative     RunnableFacility = "native"
	RunnableFacilityKubernetes RunnableFacility = "kubernetes"
)

// RunnableFacilities are every execution facility a runnable may declare.
func RunnableFacilities() []RunnableFacility {
	return []RunnableFacility{RunnableFacilityNative, RunnableFacilityKubernetes}
}

var runnableFacilityProto = map[RunnableFacility]basev0.RunnableFacility_Kind{
	RunnableFacilityNative:     basev0.RunnableFacility_NATIVE,
	RunnableFacilityKubernetes: basev0.RunnableFacility_KUBERNETES,
}

// RunnableCancellation is the YAML spelling of the declared interruption
// capability.
type RunnableCancellation string

// Interruption capabilities a runnable may declare.
const (
	RunnableCancellationNone   RunnableCancellation = "none"
	RunnableCancellationSignal RunnableCancellation = "signal"
)

var runnableCancellationProto = map[RunnableCancellation]basev0.RunnableExecution_Cancellation{
	RunnableCancellationNone:   basev0.RunnableExecution_CANCELLATION_NONE,
	RunnableCancellationSignal: basev0.RunnableExecution_CANCELLATION_SIGNAL,
}

// RunnableRecovery is the YAML spelling of the declared effect semantics: how
// an uncertain outcome is resolved.
type RunnableRecovery string

const (
	// RunnableRecoveryRecompute means the operation is pure, so re-running it
	// with the same input is safe.
	RunnableRecoveryRecompute RunnableRecovery = "recompute"
	// RunnableRecoveryReceipt means the operation has an external effect, so an
	// uncertain outcome is resolved by looking up its receipt, never by
	// re-running.
	RunnableRecoveryReceipt RunnableRecovery = "receipt"
)

var runnableRecoveryProto = map[RunnableRecovery]basev0.RunnableExecution_Recovery{
	RunnableRecoveryRecompute: basev0.RunnableExecution_RECOVERY_RECOMPUTE,
	RunnableRecoveryReceipt:   basev0.RunnableExecution_RECOVERY_RECEIPT,
}

// RunnablePayload bounds inline invocation data.
type RunnablePayload struct {
	MaxInputBytes  uint64 `yaml:"max-input-bytes,omitempty"`
	MaxOutputBytes uint64 `yaml:"max-output-bytes,omitempty"`
}

// RunnableExecution declares where a runnable may run and within which
// bounds. Unlike JobExecution there is no schedule and no retry count: a
// runnable is invoked by a caller under that caller's attempt policy, and its
// recovery policy says what an uncertain outcome may be resolved with.
type RunnableExecution struct {
	Facilities   []RunnableFacility   `yaml:"facilities"`
	Timeout      string               `yaml:"timeout"`
	Cancellation RunnableCancellation `yaml:"cancellation"`
	Recovery     RunnableRecovery     `yaml:"recovery"`
	Concurrency  uint32               `yaml:"concurrency,omitempty"`
	Payload      *RunnablePayload     `yaml:"payload,omitempty"`
}

// GetTimeout returns the declared timeout. Validate guarantees it parses.
func (e *RunnableExecution) GetTimeout() time.Duration {
	d, _ := time.ParseDuration(e.Timeout)
	return d
}

// MaxInputBytes returns the declared or default inline input bound.
func (e *RunnableExecution) MaxInputBytes() uint64 {
	if e.Payload != nil && e.Payload.MaxInputBytes > 0 {
		return e.Payload.MaxInputBytes
	}
	return DefaultRunnablePayloadBytes
}

// MaxOutputBytes returns the declared or default inline output bound.
func (e *RunnableExecution) MaxOutputBytes() uint64 {
	if e.Payload != nil && e.Payload.MaxOutputBytes > 0 {
		return e.Payload.MaxOutputBytes
	}
	return DefaultRunnablePayloadBytes
}

// Runnable is a packaged implementation of a typed finite operation. It is
// declared in runnable.codefly.yaml and owned by a module next to the
// module's services and jobs.
//
// A Runnable differs from a Job: a Job is deployment/dependency work codefly
// schedules and orders (a migration a consumer waits on), so it carries a
// schedule and retry settings. A Runnable is invoked by an orchestrator under
// that orchestrator's identity and attempt policy; codefly resolves, builds,
// packages and installs it but never schedules an invocation.
type Runnable struct {
	Kind        string `yaml:"kind"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Version is the immutable release: two runnables with the same module,
	// name and version must be the same package.
	Version string `yaml:"version"`

	PathOverride *string `yaml:"path,omitempty"`

	Agent *Agent `yaml:"agent"`

	Contract   *RunnableContract   `yaml:"contract"`
	Entrypoint *RunnableEntrypoint `yaml:"entrypoint"`
	Execution  *RunnableExecution  `yaml:"execution"`

	ServiceDependencies                []*ServiceDependency `yaml:"service-dependencies,omitempty"`
	LibraryDependencies                []*LibraryDependency `yaml:"library-dependencies,omitempty"`
	WorkspaceConfigurationDependencies []string             `yaml:"workspace-configuration-dependencies,omitempty"`

	// Spec is the agent-specific configuration.
	Spec map[string]any `yaml:"spec,omitempty"`

	// internal
	dir    string
	module string
}

// RunnableReference is used by modules to reference runnables.
type RunnableReference struct {
	Name         string  `yaml:"name"`
	Module       string  `yaml:"-"`
	PathOverride *string `yaml:"path,omitempty"`
}

// RunnableIdentity uniquely identifies a runnable release.
type RunnableIdentity struct {
	Name      string
	Module    string
	Workspace string
	Version   string
}

// Unique returns module/name.
func (r *RunnableIdentity) Unique() string {
	if r.Module == "" {
		return r.Name
	}
	return path.Join(r.Module, r.Name)
}

// NewRunnable creates a runnable declaration with a valid, empty contract
// skeleton: the author fills in the schema, handler and execution bounds.
func NewRunnable(ctx context.Context, name string) (*Runnable, error) {
	w := wool.Get(ctx).In("NewRunnable", wool.NameField(name))
	if err := validateResourcePathComponent("runnable", name); err != nil {
		return nil, w.Wrap(err)
	}
	return &Runnable{
		Kind:    RunnableKind,
		Name:    name,
		Version: "0.0.1",
		Contract: &RunnableContract{
			Protocol: RunnableProtocolV1,
			Input:    &RunnableSchema{},
			Output:   &RunnableSchema{},
		},
		Entrypoint: &RunnableEntrypoint{},
		Execution: &RunnableExecution{
			Facilities:   []RunnableFacility{RunnableFacilityNative},
			Timeout:      "5m",
			Cancellation: RunnableCancellationNone,
			Recovery:     RunnableRecoveryRecompute,
		},
	}, nil
}

// Dir returns the runnable directory.
func (r *Runnable) Dir() string {
	return r.dir
}

// WithDir sets the runnable directory.
func (r *Runnable) WithDir(dir string) {
	r.dir = dir
}

// Module returns the owning module name.
func (r *Runnable) Module() string {
	return r.module
}

// SetModule sets the owning module name.
func (r *Runnable) SetModule(module string) {
	r.module = module
}

// Unique returns module/name.
func (r *Runnable) Unique() string {
	if r.module == "" {
		return r.Name
	}
	return path.Join(r.module, r.Name)
}

// Identity returns the release identity.
func (r *Runnable) Identity() *RunnableIdentity {
	return &RunnableIdentity{Name: r.Name, Module: r.module, Version: r.Version}
}

// HandlerPath returns the absolute path of the author entrypoint.
func (r *Runnable) HandlerPath() string {
	return filepath.Join(r.dir, r.Entrypoint.Handler)
}

// Save saves the runnable configuration.
func (r *Runnable) Save(ctx context.Context) error {
	return r.SaveToDir(ctx, r.dir)
}

// SaveToDir validates the declaration and writes it to dir.
func (r *Runnable) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("Runnable.SaveToDir", wool.NameField(r.Name))
	if dir == "" {
		return w.NewError("runnable directory is empty")
	}
	if err := r.Validate(); err != nil {
		return w.Wrap(err)
	}
	return SaveToDir[Runnable](ctx, r, dir)
}

// Validate rejects an incomplete or unsupported declaration. Every rule here
// is one a language agent or launcher would otherwise have to re-check.
func (r *Runnable) Validate() error {
	if err := validateResourcePathComponent("runnable", r.Name); err != nil {
		return err
	}
	if r.Kind != RunnableKind {
		return fmt.Errorf("runnable %q has kind %q, expected %q", r.Name, r.Kind, RunnableKind)
	}
	if err := validateResourcePathOverride("runnable", r.PathOverride); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(r.Version); err != nil {
		return fmt.Errorf("runnable %q version %q is not a strict semantic version: %w", r.Name, r.Version, err)
	}
	if r.Agent == nil {
		return fmt.Errorf("runnable %q declares no agent", r.Name)
	}
	if !r.Agent.IsRunnable() {
		return fmt.Errorf("runnable %q agent kind %q is not %q", r.Name, r.Agent.Kind, RunnableAgent)
	}
	if r.Agent.Version == "" || r.Agent.Version == "latest" {
		return fmt.Errorf("runnable %q must pin its agent version; got %q", r.Name, r.Agent.Version)
	}
	if err := r.Contract.Validate(); err != nil {
		return fmt.Errorf("runnable %q contract: %w", r.Name, err)
	}
	if err := r.Entrypoint.Validate(); err != nil {
		return fmt.Errorf("runnable %q entrypoint: %w", r.Name, err)
	}
	if err := r.Execution.Validate(); err != nil {
		return fmt.Errorf("runnable %q execution: %w", r.Name, err)
	}
	for _, dep := range r.ServiceDependencies {
		if dep == nil {
			return fmt.Errorf("runnable %q contains a nil service dependency", r.Name)
		}
		if err := dep.Validate(); err != nil {
			return fmt.Errorf("runnable %q: %w", r.Name, err)
		}
	}
	return nil
}

// Validate rejects an unsupported protocol or a schema outside the bounded
// profile. A nil contract is incomplete, not empty.
func (c *RunnableContract) Validate() error {
	if c == nil {
		return fmt.Errorf("contract is required")
	}
	if !slices.Contains(RunnableProtocols(), c.Protocol) {
		return fmt.Errorf("protocol %q is not supported: expected one of %s", c.Protocol, strings.Join(RunnableProtocols(), ", "))
	}
	if c.Input == nil {
		return fmt.Errorf("input schema is required")
	}
	if err := validateRunnableFields("input", c.Input.Fields); err != nil {
		return err
	}
	if c.Output == nil {
		return fmt.Errorf("output schema is required")
	}
	return validateRunnableFields("output", c.Output.Fields)
}

func validateRunnableFields(at string, fields []*RunnableField) error {
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == nil {
			return fmt.Errorf("%s contains a nil field", at)
		}
		if !runnableFieldNamePattern.MatchString(field.Name) {
			return fmt.Errorf("%s field name %q must be an identifier", at, field.Name)
		}
		if _, exists := seen[field.Name]; exists {
			return fmt.Errorf("%s field %q is declared twice", at, field.Name)
		}
		seen[field.Name] = struct{}{}
		if err := field.validate(at + "." + field.Name); err != nil {
			return err
		}
	}
	return nil
}

func (f *RunnableField) validate(at string) error {
	if _, ok := runnableFieldTypeProto[f.Type]; !ok {
		names := make([]string, 0, len(RunnableFieldTypes()))
		for _, t := range RunnableFieldTypes() {
			names = append(names, string(t))
		}
		return fmt.Errorf("%s has type %q outside the bounded profile: expected one of %s", at, f.Type, strings.Join(names, ", "))
	}
	if f.Type != RunnableFieldObject && len(f.Fields) > 0 {
		return fmt.Errorf("%s declares fields but is not an object", at)
	}
	if f.Type != RunnableFieldArray && f.Items != nil {
		return fmt.Errorf("%s declares items but is not an array", at)
	}
	if f.Type == RunnableFieldObject {
		return validateRunnableFields(at, f.Fields)
	}
	if f.Type == RunnableFieldArray {
		if f.Items == nil {
			return fmt.Errorf("%s is an array without items", at)
		}
		if f.Items.Name != "" {
			return fmt.Errorf("%s items must not be named", at)
		}
		if f.Items.Optional {
			return fmt.Errorf("%s items cannot be optional: an array element is present or the array is shorter", at)
		}
		return f.Items.validate(at + "[]")
	}
	return nil
}

// Validate rejects a missing or escaping handler or build input.
func (e *RunnableEntrypoint) Validate() error {
	if e == nil {
		return fmt.Errorf("entrypoint is required")
	}
	if strings.TrimSpace(e.Handler) == "" {
		return fmt.Errorf("handler is required")
	}
	if err := validateResourceRelativePath("handler", e.Handler); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(e.Inputs))
	for _, input := range e.Inputs {
		if err := validateResourceRelativePath("build input", input); err != nil {
			return err
		}
		if _, exists := seen[input]; exists {
			return fmt.Errorf("build input %q is declared twice", input)
		}
		seen[input] = struct{}{}
	}
	return nil
}

// Validate rejects execution bounds a launcher could not honor.
func (e *RunnableExecution) Validate() error {
	if e == nil {
		return fmt.Errorf("execution is required")
	}
	if len(e.Facilities) == 0 {
		return fmt.Errorf("at least one facility is required")
	}
	seen := make(map[RunnableFacility]struct{}, len(e.Facilities))
	for _, facility := range e.Facilities {
		if _, ok := runnableFacilityProto[facility]; !ok {
			return fmt.Errorf("facility %q is not supported: expected one of %s", facility, joinRunnableFacilities())
		}
		if _, exists := seen[facility]; exists {
			return fmt.Errorf("facility %q is declared twice", facility)
		}
		seen[facility] = struct{}{}
	}
	if e.Timeout == "" {
		return fmt.Errorf("timeout is required: a runnable never inherits a universal timeout")
	}
	d, err := time.ParseDuration(e.Timeout)
	if err != nil {
		return fmt.Errorf("timeout %q is not a duration such as \"5m\"", e.Timeout)
	}
	if d <= 0 {
		return fmt.Errorf("timeout %q must be positive", e.Timeout)
	}
	if _, ok := runnableCancellationProto[e.Cancellation]; !ok {
		return fmt.Errorf("cancellation %q is not supported: expected %q or %q", e.Cancellation, RunnableCancellationNone, RunnableCancellationSignal)
	}
	if _, ok := runnableRecoveryProto[e.Recovery]; !ok {
		return fmt.Errorf("recovery %q is not supported: expected %q or %q", e.Recovery, RunnableRecoveryRecompute, RunnableRecoveryReceipt)
	}
	return nil
}

func joinRunnableFacilities() string {
	names := make([]string, 0, len(RunnableFacilities()))
	for _, facility := range RunnableFacilities() {
		names = append(names, string(facility))
	}
	return strings.Join(names, ", ")
}

// Proto converts the declaration to its wire form. It validates first, so a
// proto is only ever produced from a complete declaration.
func (r *Runnable) Proto(_ context.Context) (*basev0.Runnable, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	agent, err := r.Agent.Proto()
	if err != nil {
		return nil, err
	}
	proto := &basev0.Runnable{
		Name:        r.Name,
		Description: r.Description,
		Version:     r.Version,
		Agent:       agent,
		Contract:    r.Contract.Proto(),
		Handler:     r.Entrypoint.Handler,
		BuildInputs: slices.Clone(r.Entrypoint.Inputs),
		Execution:   r.Execution.Proto(),
	}
	for _, dep := range r.ServiceDependencies {
		module := dep.Module
		if module == "" {
			module = r.module
		}
		proto.ServiceDependencies = append(proto.ServiceDependencies, &basev0.ServiceReference{Name: dep.Name, Module: module})
	}
	if err := Validate(proto); err != nil {
		return nil, err
	}
	return proto, nil
}

// Proto converts the contract to its wire form. Validate the contract first.
func (c *RunnableContract) Proto() *basev0.RunnableContract {
	return &basev0.RunnableContract{
		Protocol: c.Protocol,
		Input:    &basev0.RunnableSchema{Fields: runnableFieldsProto(c.Input.Fields)},
		Output:   &basev0.RunnableSchema{Fields: runnableFieldsProto(c.Output.Fields)},
	}
}

func runnableFieldsProto(fields []*RunnableField) []*basev0.RunnableField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]*basev0.RunnableField, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.proto())
	}
	return out
}

func (f *RunnableField) proto() *basev0.RunnableField {
	out := &basev0.RunnableField{
		Name:        f.Name,
		Type:        runnableFieldTypeProto[f.Type],
		Description: f.Description,
		Optional:    f.Optional,
		Nullable:    f.Nullable,
		Fields:      runnableFieldsProto(f.Fields),
	}
	if f.Items != nil {
		out.Items = f.Items.proto()
	}
	return out
}

// RunnableContractFromProto converts a wire contract back to its declaration
// form and validates it, so a contract carried by a package descriptor is held
// to the same bounded profile as one read from YAML.
func RunnableContractFromProto(contract *basev0.RunnableContract) (*RunnableContract, error) {
	if contract == nil {
		return nil, fmt.Errorf("contract is required")
	}
	out := &RunnableContract{
		Protocol: contract.GetProtocol(),
		Input:    &RunnableSchema{Fields: runnableFieldsFromProto(contract.GetInput().GetFields())},
		Output:   &RunnableSchema{Fields: runnableFieldsFromProto(contract.GetOutput().GetFields())},
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func runnableFieldsFromProto(fields []*basev0.RunnableField) []*RunnableField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]*RunnableField, 0, len(fields))
	for _, field := range fields {
		out = append(out, runnableFieldFromProto(field))
	}
	return out
}

func runnableFieldFromProto(field *basev0.RunnableField) *RunnableField {
	out := &RunnableField{
		Name:        field.GetName(),
		Type:        runnableFieldTypeFromProto(field.GetType()),
		Description: field.GetDescription(),
		Optional:    field.GetOptional(),
		Nullable:    field.GetNullable(),
		Fields:      runnableFieldsFromProto(field.GetFields()),
	}
	if field.GetItems() != nil {
		out.Items = runnableFieldFromProto(field.GetItems())
	}
	return out
}

// runnableFieldTypeFromProto maps a wire type back to its YAML spelling. An
// unknown wire type maps to its enum name so validation reports it as outside
// the profile instead of silently becoming a string.
func runnableFieldTypeFromProto(t basev0.RunnableField_Type) RunnableFieldType {
	for yamlType, protoType := range runnableFieldTypeProto {
		if protoType == t {
			return yamlType
		}
	}
	return RunnableFieldType(strings.ToLower(t.String()))
}

// Proto converts the execution declaration to its wire form, with payload
// defaults applied. Validate the execution first.
func (e *RunnableExecution) Proto() *basev0.RunnableExecution {
	out := &basev0.RunnableExecution{
		Timeout:        durationpb.New(e.GetTimeout()),
		Cancellation:   runnableCancellationProto[e.Cancellation],
		Recovery:       runnableRecoveryProto[e.Recovery],
		MaxInputBytes:  e.MaxInputBytes(),
		MaxOutputBytes: e.MaxOutputBytes(),
		Concurrency:    e.Concurrency,
	}
	for _, facility := range e.Facilities {
		out.Facilities = append(out.Facilities, &basev0.RunnableFacility{Kind: runnableFacilityProto[facility]})
	}
	return out
}

// LoadRunnableFromDir loads and validates a runnable from a directory.
func LoadRunnableFromDir(ctx context.Context, dir string) (*Runnable, error) {
	w := wool.Get(ctx).In("LoadRunnableFromDir", wool.DirField(dir))
	r, err := LoadFromDir[Runnable](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if err := r.Validate(); err != nil {
		return nil, w.Wrap(err)
	}
	r.dir = dir
	return r, nil
}

// Module runnable management

// RunnablePath returns the absolute path of a runnable reference, following
// the same override rules as services and jobs.
func (mod *Module) RunnablePath(ref *RunnableReference) string {
	if ref.PathOverride == nil {
		return path.Join(mod.Dir(), "runnables", ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(mod.Dir(), *ref.PathOverride)
}

// LoadRunnableFromReference loads a runnable from a reference.
func (mod *Module) LoadRunnableFromReference(ctx context.Context, ref *RunnableReference) (*Runnable, error) {
	if err := validateRunnableReferencePath(ref); err != nil {
		return nil, wool.Get(ctx).In("Module.LoadRunnableFromReference").Wrap(err)
	}
	w := wool.Get(ctx).In("Module.LoadRunnableFromReference", wool.NameField(ref.Name))
	r, err := LoadRunnableFromDir(ctx, mod.RunnablePath(ref))
	if err != nil {
		return nil, w.Wrap(err)
	}
	if r.Name != ref.Name {
		return nil, w.NewError("runnable referenced as <%s> declares name <%s> in %s", ref.Name, r.Name, r.dir)
	}
	r.module = mod.Name
	return r, nil
}

// LoadRunnableFromName loads a runnable by name from a module.
func (mod *Module) LoadRunnableFromName(ctx context.Context, name string) (*Runnable, error) {
	w := wool.Get(ctx).In("Module.LoadRunnableFromName", wool.NameField(name))
	if err := validateResourcePathComponent("runnable", name); err != nil {
		return nil, w.Wrap(err)
	}
	for _, ref := range mod.RunnableReferences {
		if ReferenceMatch(ref.Name, name) {
			return mod.LoadRunnableFromReference(ctx, ref)
		}
	}
	return nil, w.Wrap(shared.NewErrorResourceNotFound("runnable", name))
}

// LoadRunnables loads every runnable the module references.
func (mod *Module) LoadRunnables(ctx context.Context) ([]*Runnable, error) {
	var runnables []*Runnable
	for _, ref := range mod.RunnableReferences {
		r, err := mod.LoadRunnableFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		runnables = append(runnables, r)
	}
	return runnables, nil
}

// ExistsRunnable returns true if the module references the runnable.
func (mod *Module) ExistsRunnable(name string) bool {
	for _, ref := range mod.RunnableReferences {
		if ref.Name == name {
			return true
		}
	}
	return false
}

// AddRunnableReference adds a runnable reference to the module.
func (mod *Module) AddRunnableReference(ctx context.Context, ref *RunnableReference) error {
	if err := validateRunnableReferencePath(ref); err != nil {
		return wool.Get(ctx).In("Module.AddRunnableReference").Wrap(err)
	}
	w := wool.Get(ctx).In("Module.AddRunnableReference", wool.NameField(ref.Name))
	if mod.ExistsRunnable(ref.Name) {
		return w.NewError("runnable %s already exists in module", ref.Name)
	}
	ref.Module = mod.Name
	mod.RunnableReferences = append(mod.RunnableReferences, ref)
	return nil
}

// NewRunnable creates a runnable directory in the module and references it.
// The declaration is returned unsaved: it is incomplete until the author's
// agent fills the contract, and SaveToDir refuses an incomplete one.
func (mod *Module) NewRunnable(ctx context.Context, name string) (*Runnable, error) {
	w := wool.Get(ctx).In("Module.NewRunnable", wool.NameField(name))
	if mod.ExistsRunnable(name) {
		return nil, w.NewError("runnable %s already exists in module %s", name, mod.Name)
	}
	r, err := NewRunnable(ctx, name)
	if err != nil {
		return nil, w.Wrap(err)
	}
	dir := path.Join(mod.Dir(), "runnables", name)
	if _, err := shared.CheckDirectoryOrCreate(ctx, dir); err != nil {
		return nil, w.Wrapf(err, "failed to create runnable directory")
	}
	r.dir = dir
	r.module = mod.Name
	if err := mod.AddRunnableReference(ctx, &RunnableReference{Name: name}); err != nil {
		return nil, w.Wrap(err)
	}
	return r, nil
}

// Workspace runnable management

// LoadRunnableFromUnique loads a runnable by module/name.
func (workspace *Workspace) LoadRunnableFromUnique(ctx context.Context, unique string) (*Runnable, error) {
	w := wool.Get(ctx).In("Workspace.LoadRunnableFromUnique", wool.Field("unique", unique))
	moduleName, name := SplitUnique(unique)
	mod, err := workspace.LoadModuleFromName(ctx, moduleName)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return mod.LoadRunnableFromName(ctx, name)
}

// LoadAllRunnables loads every runnable from every module. A module that
// cannot be loaded fails the call: a missing runnable is not a warning when
// the caller is about to build or install what it finds.
func (workspace *Workspace) LoadAllRunnables(ctx context.Context) ([]*Runnable, error) {
	w := wool.Get(ctx).In("Workspace.LoadAllRunnables")
	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	var all []*Runnable
	for _, mod := range modules {
		runnables, err := mod.LoadRunnables(ctx)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load runnables of module %s", mod.Name)
		}
		all = append(all, runnables...)
	}
	return all, nil
}

// FindRunnableByName finds a runnable by module/name or by bare name across
// modules; a bare name matching several modules is ambiguous.
func (workspace *Workspace) FindRunnableByName(ctx context.Context, name string) (*Runnable, error) {
	w := wool.Get(ctx).In("Workspace.FindRunnableByName", wool.NameField(name))
	if moduleName, runnableName := SplitUnique(name); moduleName != "" {
		return workspace.LoadRunnableFromUnique(ctx, path.Join(moduleName, runnableName))
	}
	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	var found *Runnable
	for _, mod := range modules {
		if !mod.ExistsRunnable(name) {
			continue
		}
		r, err := mod.LoadRunnableFromName(ctx, name)
		if err != nil {
			return nil, w.Wrap(err)
		}
		if found != nil {
			return nil, w.NewError("ambiguous runnable name %s: found in modules %s and %s", name, found.module, mod.Name)
		}
		found = r
	}
	if found == nil {
		return nil, w.Wrap(shared.NewErrorResourceNotFound("runnable", name))
	}
	return found, nil
}

// DiscoverRunnables lists the runnables under moduleDir/runnables that carry
// a declaration, as references to add to the module.
func DiscoverRunnables(ctx context.Context, moduleDir string) ([]*RunnableReference, error) {
	w := wool.Get(ctx).In("DiscoverRunnables", wool.DirField(moduleDir))
	runnablesDir := filepath.Join(moduleDir, "runnables")
	exists, err := shared.DirectoryExists(ctx, runnablesDir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !exists {
		return nil, nil
	}
	entries, err := os.ReadDir(runnablesDir)
	if err != nil {
		return nil, w.Wrapf(err, "failed to read runnables directory")
	}
	var refs []*RunnableReference
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(runnablesDir, entry.Name(), RunnableConfigurationName)); err != nil {
			continue
		}
		refs = append(refs, &RunnableReference{Name: entry.Name()})
	}
	return refs, nil
}
