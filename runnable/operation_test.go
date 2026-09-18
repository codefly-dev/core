package runnable_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const applyText = "/documents.ingest.v1.IngestionService/ApplyText"

// declaredOperation is the policy document-store installs for its text
// operation, written as the option the method would carry.
func declaredOperation() *runnablev0.Operation {
	return &runnablev0.Operation{
		AttemptTimeout: durationpb.New(10 * time.Second),
		TotalTimeout:   durationpb.New(time.Minute),
		MaxAttempts:    1,
		Backoff:        durationpb.New(time.Second),
		RetryableCodes: []string{"UNAVAILABLE"},
		Audience:       "documents.ingestion",
		InvokeScopes:   []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"ingest", "read"}}},
		LookupScopes:   []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"read"}}},
		LookupMethod:   "/documents.ingest.v1.IngestionService/LookupText",
	}
}

// ingestion mirrors module-document-store's published ingestion schema, whose
// hand-written Runnable package is the oracle this derivation has to reproduce.
func ingestion(declared *runnablev0.Operation, methods ...*descriptorpb.MethodDescriptorProto) fileSpec {
	request := message("ApplyTextRequest",
		scalar("origin", 1, tString),
		scalar("container", 2, tString),
		scalar("ref", 3, tString),
		scalar("path", 4, tString),
		scalar("revision", 5, tString),
		scalar("commit", 6, tString),
		scalar("cursor", 7, tInt64),
		scalar("content_type", 8, tString),
		scalar("text", 9, tString),
	)
	response := message("ApplyTextResponse",
		scalar("reference", 1, tString),
		scalar("content_hash", 2, tString),
		scalar("effect_digest", 3, tString),
		scalar("minted", 4, tBool),
		scalar("skipped", 5, tBool),
		scalar("quarantined", 6, tBool),
	)
	lookupRequest := message("LookupTextRequest", named("original", 1, tMessage, ".documents.ingest.v1.ApplyTextRequest"))
	lookupResponse := message("LookupTextResponse",
		scalar("found", 1, tBool),
		named("result", 2, tMessage, ".documents.ingest.v1.ApplyTextResponse"),
	)
	if len(methods) == 0 {
		methods = []*descriptorpb.MethodDescriptorProto{operationMethod("ApplyText",
			".documents.ingest.v1.ApplyTextRequest", ".documents.ingest.v1.ApplyTextResponse", declared)}
	}
	// The paired receipt lookup is part of every fixture: an operation naming a
	// lookup its service does not publish is now rejected, as it should be.
	methods = append(methods, operationMethod("LookupText",
		".documents.ingest.v1.LookupTextRequest", ".documents.ingest.v1.LookupTextResponse", nil))
	return fileSpec{
		name:     "documents/ingest/v1/ingestion.proto",
		pkg:      "documents.ingest.v1",
		imports:  []string{"codefly/runnable/v0/options.proto"},
		messages: []*descriptorpb.DescriptorProto{request, response, lookupRequest, lookupResponse},
		services: []*descriptorpb.ServiceDescriptorProto{service("IngestionService", methods...)},
	}
}

func ingestionFiles(t *testing.T, declared *runnablev0.Operation, methods ...*descriptorpb.MethodDescriptorProto) *protoregistry.Files {
	t.Helper()
	return registry(t, compile(t, ingestion(declared, methods...)))
}

func ingestLocation() *resources.RunnableLocation {
	return &resources.RunnableLocation{
		Identity:            &resources.RunnableIdentity{Name: "ingest-text", Module: "documents", Workspace: "module-document-store", Version: "0.1.0"},
		WorkspacePath:       "/workspaces/module-document-store",
		RelativeToWorkspace: "modules/documents",
	}
}

func ingestOwner() runnable.ServiceOwner {
	return runnable.ServiceOwner{
		Module:   "documents",
		Service:  "runtime-worker",
		Endpoint: "grpc",
		Agent:    &basev0.Agent{Kind: basev0.Agent_SERVICE, Name: "go", Version: "0.0.48", Publisher: "codefly.dev"},
	}
}

func derivedIngestion(t *testing.T) (*basev0.RunnablePackage, *runnable.OperationSpec) {
	t.Helper()
	pkg, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	return pkg, spec
}

func TestPackageFromMethodIsByteStable(t *testing.T) {
	pkg, _ := derivedIngestion(t)
	require.NoError(t, runnable.VerifyPackage(pkg))
	require.Equal(t, "4c26fb95a0236b94107b30f34783cd893f2be36e0d157340c247013fd93a9a1f", pkg.GetDigest())

	again, _ := derivedIngestion(t)
	require.True(t, proto.Equal(pkg, again))
}

// TestPackageFromMethodReproducesTheHandWrittenOracle is the proof this is a
// lift rather than a rewrite: module-document-store already publishes this
// operation through a package its worker assembles by hand
// (ingestrunnable.Package, printed by the worker's --describe-runnable), and
// deriving it from the method descriptor has to land on the same bytes.
//
// The two inline payload bounds are the exception, and deliberately so: the
// owner picked those numbers, nothing in the descriptor says them, and this
// builder states the contract's defaults rather than inventing an owner's
// budget. Everything the method does determine is compared as it stands.
func TestPackageFromMethodReproducesTheHandWrittenOracle(t *testing.T) {
	handWritten, err := runnable.PreparePackage(&basev0.RunnablePackage{
		Schema:   runnable.PackageSchemaV1,
		Identity: &basev0.RunnableIdentity{Workspace: "module-document-store", Module: "documents", Name: "ingest-text", Version: "0.1.0"},
		Agent:    &basev0.Agent{Kind: basev0.Agent_SERVICE, Name: "go", Version: "0.0.48", Publisher: "codefly.dev"},
		Contract: &basev0.RunnableContract{
			Protocol: resources.RunnableServiceProtocolV1,
			Input: &basev0.RunnableSchema{Fields: []*basev0.RunnableField{
				{Name: "origin", Type: basev0.RunnableField_STRING},
				{Name: "container", Type: basev0.RunnableField_STRING},
				{Name: "ref", Type: basev0.RunnableField_STRING},
				{Name: "path", Type: basev0.RunnableField_STRING},
				{Name: "revision", Type: basev0.RunnableField_STRING},
				{Name: "commit", Type: basev0.RunnableField_STRING},
				{Name: "cursor", Type: basev0.RunnableField_INTEGER},
				{Name: "content_type", Type: basev0.RunnableField_STRING},
				{Name: "text", Type: basev0.RunnableField_STRING},
			}},
			Output: &basev0.RunnableSchema{Fields: []*basev0.RunnableField{
				{Name: "reference", Type: basev0.RunnableField_STRING},
				{Name: "content_hash", Type: basev0.RunnableField_STRING},
				{Name: "effect_digest", Type: basev0.RunnableField_STRING},
				{Name: "minted", Type: basev0.RunnableField_BOOLEAN},
				{Name: "skipped", Type: basev0.RunnableField_BOOLEAN},
				{Name: "quarantined", Type: basev0.RunnableField_BOOLEAN},
			}},
		},
		Execution: &basev0.RunnableExecution{
			Facilities:     []*basev0.RunnableFacility{{Kind: basev0.RunnableFacility_SERVICE}},
			Timeout:        durationpb.New(time.Minute),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_NONE,
			Recovery:       basev0.RunnableExecution_RECOVERY_RECEIPT,
			MaxInputBytes:  resources.DefaultRunnablePayloadBytes,
			MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
		},
		ServiceOperations: []*basev0.RunnableServiceOperation{{
			Module: "documents", Name: "runtime-worker", Endpoint: "grpc",
			Operation:     applyText,
			InputMessage:  "documents.ingest.v1.ApplyTextRequest",
			OutputMessage: "documents.ingest.v1.ApplyTextResponse",
			Adaptation:    basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1,
		}},
	})
	require.NoError(t, err)

	derived, spec := derivedIngestion(t)
	require.True(t, proto.Equal(handWritten, derived), "derived %v", derived)

	// The policy and authority stay beside the package, never inside it.
	require.Equal(t, applyText, spec.Method)
	require.Equal(t, 10*time.Second, spec.AttemptTimeout)
	require.Equal(t, time.Minute, spec.TotalTimeout)
	require.EqualValues(t, 1, spec.MaxAttempts)
	require.Equal(t, time.Second, spec.Backoff)
	require.Equal(t, []string{"UNAVAILABLE"}, spec.RetryableCodes)
	require.Equal(t, "documents.ingestion", spec.Audience)
	require.Equal(t, "/documents.ingest.v1.IngestionService/LookupText", spec.LookupMethod)
}

func TestOperationFromMethodSkipsAnUnmarkedMethod(t *testing.T) {
	files := ingestionFiles(t, nil, operationMethod("ApplyText",
		".documents.ingest.v1.ApplyTextRequest", ".documents.ingest.v1.ApplyTextResponse", nil))
	_, _, err := runnable.PackageFromMethod(files, ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrNotAnOperation)
	require.ErrorContains(t, err, "documents.ingest.v1.IngestionService.ApplyText")
}

func TestOperationFromMethodRejectsStreaming(t *testing.T) {
	for _, streaming := range []string{"client", "server"} {
		t.Run(streaming, func(t *testing.T) {
			method := operationMethod("ApplyText",
				".documents.ingest.v1.ApplyTextRequest", ".documents.ingest.v1.ApplyTextResponse", declaredOperation())
			if streaming == "client" {
				method.ClientStreaming = proto.Bool(true)
			} else {
				method.ServerStreaming = proto.Bool(true)
			}
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, nil, method), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, applyText+" streams")
		})
	}
}

func TestOperationFromMethodEnforcesThePolicyBounds(t *testing.T) {
	documents := func(actions ...string) []*basev0.WorkScopeV1 {
		return []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: actions}}
	}
	for _, test := range []struct {
		name    string
		mutate  func(*runnablev0.Operation)
		because string
	}{
		{"attempt too short", func(o *runnablev0.Operation) { o.AttemptTimeout = durationpb.New(999 * time.Millisecond) }, "attempt_timeout"},
		{"attempt too long", func(o *runnablev0.Operation) {
			o.AttemptTimeout = durationpb.New(2 * time.Minute)
			o.TotalTimeout = durationpb.New(2 * time.Minute)
		}, "attempt_timeout"},
		{"total shorter than attempt", func(o *runnablev0.Operation) { o.TotalTimeout = durationpb.New(time.Second) }, "shorter than one attempt"},
		{"no attempt", func(o *runnablev0.Operation) { o.MaxAttempts = 0 }, "max_attempts"},
		{"too many attempts", func(o *runnablev0.Operation) { o.MaxAttempts = 6 }, "max_attempts"},
		{"backoff too short", func(o *runnablev0.Operation) { o.Backoff = durationpb.New(50 * time.Millisecond) }, "backoff"},
		{"backoff too long", func(o *runnablev0.Operation) { o.Backoff = durationpb.New(2 * time.Minute) }, "backoff"},
		{"too many retryable codes", func(o *runnablev0.Operation) {
			o.RetryableCodes = make([]string, runnable.MaxRetryableCodes+1)
			for i := range o.RetryableCodes {
				o.RetryableCodes[i] = "UNAVAILABLE"
			}
		}, "retryable codes"},
		{"unknown retryable code", func(o *runnablev0.Operation) { o.RetryableCodes = []string{"FLAKY"} }, `"FLAKY"`},
		{"lowercase retryable code", func(o *runnablev0.Operation) { o.RetryableCodes = []string{"unavailable"} }, `"unavailable"`},
		{"repeated retryable code", func(o *runnablev0.Operation) { o.RetryableCodes = []string{"UNAVAILABLE", "UNAVAILABLE"} }, "twice"},
		{"scope without a kind", func(o *runnablev0.Operation) { o.InvokeScopes = []*basev0.WorkScopeV1{{Actions: []string{"ingest"}}} }, "is not a usable name"},
		{"scope without an action", func(o *runnablev0.Operation) { o.InvokeScopes = documents() }, "names 0 actions"},
		{"scope declared twice", func(o *runnablev0.Operation) {
			o.InvokeScopes = append(o.InvokeScopes, &basev0.WorkScopeV1{ResourceKind: "documents", Actions: []string{"read"}})
		}, "twice"},
		{"lookup scope is not read-only", func(o *runnablev0.Operation) { o.LookupScopes = documents("read", "ingest") }, "not read-only"},
		{"lookup scope writes", func(o *runnablev0.Operation) {
			o.InvokeScopes = documents("ingest", "purge")
			o.LookupScopes = documents("purge")
		}, "not read-only"},
		{"lookup scope reaches another kind", func(o *runnablev0.Operation) {
			o.LookupScopes = []*basev0.WorkScopeV1{{ResourceKind: "receipts", Actions: []string{"read"}}}
		}, "not covered"},
		{"lookup scope holds an unheld action", func(o *runnablev0.Operation) { o.InvokeScopes = documents("ingest") }, "not covered"},
		{"lookup scope widens explicit ids", func(o *runnablev0.Operation) {
			o.InvokeScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"ingest", "read"}, ResourceIds: []string{"one"}}}
			o.LookupScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"read"}, ResourceIds: []string{"two"}}}
		}, "not covered"},
		{"lookup scope widens a narrowed set", func(o *runnablev0.Operation) {
			o.InvokeScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"ingest", "read"}, ResourceIds: []string{"one"}}}
		}, "not covered"},
		// The runtime refuses to install an operation with no audience and no
		// scopes, so a descriptor declaring none must not generate one.
		{"no audience", func(o *runnablev0.Operation) { o.Audience = "" }, "audience"},
		{"untrimmed audience", func(o *runnablev0.Operation) { o.Audience = " documents.ingestion " }, "audience"},
		{"overlong audience", func(o *runnablev0.Operation) { o.Audience = strings.Repeat("a", runnable.MaxAudienceLength+1) }, "audience"},
		{"no invoke scopes", func(o *runnablev0.Operation) { o.InvokeScopes = nil }, "declares 0 invoke_scopes"},
		{"no lookup scopes", func(o *runnablev0.Operation) { o.LookupScopes = nil }, "declares 0 lookup_scopes"},
		{"too many scopes", func(o *runnablev0.Operation) {
			o.InvokeScopes = make([]*basev0.WorkScopeV1, 0, runnable.MaxScopes+1)
			for i := range runnable.MaxScopes + 1 {
				o.InvokeScopes = append(o.InvokeScopes, &basev0.WorkScopeV1{ResourceKind: fmt.Sprintf("kind-%d", i), Actions: []string{"read"}})
			}
		}, "invoke_scopes"},
		{"blank action", func(o *runnablev0.Operation) { o.InvokeScopes = documents("ingest", "read", " ") }, `action " "`},
		{"action with a newline", func(o *runnablev0.Operation) { o.InvokeScopes = documents("ingest", "read", "a\nb") }, "not a usable value"},
		{"repeated action", func(o *runnablev0.Operation) { o.InvokeScopes = documents("ingest", "read", "read") }, "twice"},
		{"too many resource ids", func(o *runnablev0.Operation) {
			ids := make([]string, 0, runnable.MaxScopeResourceIds+1)
			for i := range runnable.MaxScopeResourceIds + 1 {
				ids = append(ids, fmt.Sprintf("doc-%d", i))
			}
			o.InvokeScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"ingest", "read"}, ResourceIds: ids}}
		}, "resource ids"},
		{"repeated resource id", func(o *runnablev0.Operation) {
			o.InvokeScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"ingest", "read"}, ResourceIds: []string{"one", "one"}}}
		}, "twice"},
		// AsDuration saturates instead of reporting, so an unreadable duration
		// used to become 292 years.
		{"unreadable duration", func(o *runnablev0.Operation) {
			o.TotalTimeout = &durationpb.Duration{Seconds: 999999999999999}
		}, "is not a valid duration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared := declaredOperation()
			test.mutate(declared)
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
			require.ErrorContains(t, err, applyText)
		})
	}
}

func TestOperationFromMethodAcceptsAWildcardParentNarrowedByLookup(t *testing.T) {
	declared := declaredOperation()
	declared.LookupScopes = []*basev0.WorkScopeV1{{ResourceKind: "documents", Actions: []string{"read"}, ResourceIds: []string{"one"}}}
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, []string{"one"}, spec.LookupScopes[0].GetResourceIds())
}

func TestPackageFromMethodRejectsAnUnresolvableMethod(t *testing.T) {
	for _, test := range []struct {
		name, method, because string
	}{
		{"not a method name", "documents.ingest.v1.IngestionService.ApplyText", "of the form"},
		{"no method", "/documents.ingest.v1.IngestionService/", "of the form"},
		{"unknown service", "/documents.ingest.v1.Missing/ApplyText", "not found"},
		{"unknown method", "/documents.ingest.v1.IngestionService/Missing", "publishes no method Missing"},
		{"not a service", "/documents.ingest.v1.ApplyTextRequest/ApplyText", "is not a service"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), ingestLocation(), ingestOwner(), test.method)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}
}

func TestPackageFromMethodRejectsAPayloadOutsideTheProfile(t *testing.T) {
	for _, test := range []struct {
		name    string
		message int
		path    string
	}{
		{"request", 0, "documents.ingest.v1.ApplyTextRequest.size"},
		{"response", 1, "documents.ingest.v1.ApplyTextResponse.size"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := ingestion(declaredOperation())
			spec.messages[test.message].Field = append(spec.messages[test.message].Field, scalar("size", 10, tUint64))
			_, _, err := runnable.PackageFromMethod(registry(t, compile(t, spec)), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.path)
		})
	}
}

func TestPackageFromMethodRequiresAReleaseIdentity(t *testing.T) {
	_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), &resources.RunnableLocation{}, ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "release identity")
}

func TestPackageFromMethodRejectsAnUnpinnedOwnerAgent(t *testing.T) {
	owner := ingestOwner()
	owner.Agent = &basev0.Agent{Kind: basev0.Agent_SERVICE, Name: "go", Version: "latest", Publisher: "codefly.dev"}
	_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), ingestLocation(), owner, applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "must be pinned")
}

func TestOperationFromMethodRequiresADescriptor(t *testing.T) {
	_, err := runnable.OperationFromMethod(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.True(t, strings.Contains(err.Error(), "method descriptor is required"))
}

// TestOperationFromMethodValidatesThePairedLookupMethod guards the one
// declaration that, wrong, makes recovery repeat the effect it exists to avoid.
func TestOperationFromMethodValidatesThePairedLookupMethod(t *testing.T) {
	for _, test := range []struct {
		name, lookup, because string
	}{
		{"itself", applyText, "names itself as its lookup method"},
		{"another service", "/somewhere.Else/Entirely", "does not publish"},
		{"a bare method name", "LookupText", "does not publish"},
		{"a method the service does not publish", "/documents.ingest.v1.IngestionService/DoesNotExist", "does not publish"},
		{"whitespace", "   ", "does not publish"},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared := declaredOperation()
			declared.LookupMethod = test.lookup
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}

	// A streaming lookup cannot answer with one receipt.
	streaming := operationMethod("LookupText", ".documents.ingest.v1.LookupTextRequest", ".documents.ingest.v1.LookupTextResponse", nil)
	streaming.ServerStreaming = proto.Bool(true)
	spec := ingestion(declaredOperation())
	spec.services[0].Method[1] = streaming
	_, _, err := runnable.PackageFromMethod(registry(t, compile(t, spec)), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "which streams")

	// An empty lookup method leaves the answer to the SDK's generic lookup.
	declared := declaredOperation()
	declared.LookupMethod = ""
	_, got, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Empty(t, got.LookupMethod)
}

func TestOperationFromMethodRejectsAnEmptyPolicy(t *testing.T) {
	_, _, err := runnable.PackageFromMethod(ingestionFiles(t, &runnablev0.Operation{}), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "declares no execution policy")
}

// TestPackageFromMethodNamesTheOwnerServiceModule keeps the published method's
// module the owner's own: it is resolved against that service's Endpoint, and a
// runnable declared in another module would name a service no binding can find.
func TestPackageFromMethodNamesTheOwnerServiceModule(t *testing.T) {
	location := ingestLocation()
	location.Identity.Module = "ingestion"
	pkg, _, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), location, ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, "ingestion", pkg.GetIdentity().GetModule())
	require.Equal(t, "documents", pkg.GetServiceOperations()[0].GetModule())
}

// TestPackageFromMethodRejectsAWellKnownPayload covers the payload itself, not
// just its fields: a Timestamp reads as two integers and an Empty as an object
// with no keys, either of which is a contract that misdescribes the wire.
func TestPackageFromMethodRejectsAWellKnownPayload(t *testing.T) {
	for _, wellKnown := range []string{
		".google.protobuf.Empty",
		".google.protobuf.Timestamp",
		".google.protobuf.Duration",
		".google.protobuf.StringValue",
	} {
		t.Run(wellKnown, func(t *testing.T) {
			spec := ingestion(declaredOperation())
			spec.imports = append(spec.imports, "google/protobuf/empty.proto", "google/protobuf/timestamp.proto",
				"google/protobuf/duration.proto", "google/protobuf/wrappers.proto")
			spec.services[0].Method[0].OutputType = proto.String(wellKnown)
			_, _, err := runnable.PackageFromMethod(registry(t, compile(t, spec)), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, "payload "+strings.TrimPrefix(wellKnown, "."))
			require.ErrorContains(t, err, "well-known type")
		})
	}
}
