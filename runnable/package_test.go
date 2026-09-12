package runnable_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const (
	digestA = "sha256:" + "a1" + "00000000000000000000000000000000000000000000000000000000000000"
	digestB = "sha256:" + "b2" + "00000000000000000000000000000000000000000000000000000000000000"
	digestC = "sha256:" + "c3" + "00000000000000000000000000000000000000000000000000000000000000"
)

func loadedContract(t *testing.T) *basev0.RunnableContract {
	t.Helper()
	r, err := resources.LoadRunnableFromDir(context.Background(), "../resources/testdata/workspaces/with-runnables/runnables/word-count")
	require.NoError(t, err)
	return r.Contract.Proto()
}

func samplePackage(t *testing.T) *basev0.RunnablePackage {
	t.Helper()
	return &basev0.RunnablePackage{
		Schema:   runnable.PackageSchemaV1,
		Identity: &basev0.RunnableIdentity{Name: "word-count", Module: "with-runnables", Workspace: "qualification", Version: "0.1.0"},
		Agent:    &basev0.Agent{Kind: basev0.Agent_RUNNABLE, Name: "python", Publisher: "codefly.dev", Version: "0.0.1"},
		Contract: loadedContract(t),
		Execution: &basev0.RunnableExecution{
			Facilities: []*basev0.RunnableFacility{
				{Kind: basev0.RunnableFacility_KUBERNETES},
				{Kind: basev0.RunnableFacility_NATIVE},
			},
			Timeout:        durationpb.New(2 * time.Minute),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_SIGNAL,
			Recovery:       basev0.RunnableExecution_RECOVERY_RECOMPUTE,
			MaxInputBytes:  65536,
			MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
		},
		Build: &basev0.RunnableBuild{
			Handler: &basev0.RunnableInputDigest{Path: "handler.py", Digest: digestA},
			Inputs: []*basev0.RunnableInputDigest{
				{Path: "uv.lock", Digest: digestB},
				{Path: "pyproject.toml", Digest: digestC},
			},
			HarnessDigest:       digestA,
			Toolchain:           "python-3.12.4",
			ConfigurationDigest: digestB,
		},
		Artifacts: []*basev0.RunnableArtifact{
			{Kind: basev0.RunnableArtifact_IMAGE, Platform: "linux/amd64", Reference: "ghcr.io/example/word-count:0.1.0@" + digestB, Digest: digestB},
			{Kind: basev0.RunnableArtifact_NATIVE, Platform: "darwin/arm64", Reference: "word-count-0.1.0-darwin-arm64.tar.gz", Digest: digestA, Command: []string{"python", "-m", "codefly_runnable.harness"}},
		},
		ServiceDependencies:                []*basev0.RunnableDependency{{Name: "store", Module: "with-runnables", Kind: "runtime", Endpoints: []string{"tcp"}}},
		WorkspaceConfigurationDependencies: []string{"openai", "artifact-store"},
	}
}

func preparedPackage(t *testing.T) *basev0.RunnablePackage {
	t.Helper()
	pkg, err := runnable.PreparePackage(samplePackage(t))
	require.NoError(t, err)
	return pkg
}

func TestPreparePackageIsCanonicalAndDeterministic(t *testing.T) {
	first := preparedPackage(t)
	require.Len(t, first.GetDigest(), 64)
	require.NoError(t, runnable.VerifyPackage(first))

	// Build inputs sort by path, artifacts by kind then platform.
	require.Equal(t, "pyproject.toml", first.GetBuild().GetInputs()[0].GetPath())
	require.Equal(t, basev0.RunnableArtifact_NATIVE, first.GetArtifacts()[0].GetKind())

	// The same content in another order is the same package.
	reordered := samplePackage(t)
	reordered.Artifacts[0], reordered.Artifacts[1] = reordered.Artifacts[1], reordered.Artifacts[0]
	reordered.Execution.Facilities[0], reordered.Execution.Facilities[1] = reordered.Execution.Facilities[1], reordered.Execution.Facilities[0]
	reordered.WorkspaceConfigurationDependencies = []string{"artifact-store", "openai"}
	reordered.ServiceDependencies = append(reordered.ServiceDependencies, &basev0.RunnableDependency{Name: "cache", Module: "aaa", Endpoints: []string{"b", "a"}})
	first.ServiceDependencies = append([]*basev0.RunnableDependency{{Name: "cache", Module: "aaa", Endpoints: []string{"a", "b"}}}, first.ServiceDependencies...)
	first.Digest = ""
	second, err := runnable.PreparePackage(reordered)
	require.NoError(t, err)
	third, err := runnable.PreparePackage(first)
	require.NoError(t, err)
	require.Equal(t, second.GetDigest(), third.GetDigest())
	require.True(t, proto.Equal(second, third))

	// The digest is a property of the canonical form, not of this binary's
	// wire encoding, and is mixed with its format identifier.
	require.Equal(t, "64ca051ef0b57bdc8199500653e25d6e0a620c2177aefddf6425c0d5ba6fba83", preparedPackage(t).GetDigest())

	// The input is never mutated, and a supplied digest must match.
	original := samplePackage(t)
	prepared, err := runnable.PreparePackage(original)
	require.NoError(t, err)
	require.Empty(t, original.GetDigest())
	require.NotEqual(t, prepared.GetDigest(), "")
	original.Digest = strings.Repeat("0", 64)
	_, err = runnable.PreparePackage(original)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "does not match")
}

func TestVerifyPackageRejectsTamperedBytes(t *testing.T) {
	pkg := preparedPackage(t)
	require.ErrorContains(t, runnable.VerifyPackage(samplePackage(t)), "carries no digest")

	tampered := proto.Clone(pkg).(*basev0.RunnablePackage)
	tampered.Build.Handler.Digest = digestC
	err := runnable.VerifyPackage(tampered)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	uncanonical := proto.Clone(pkg).(*basev0.RunnablePackage)
	uncanonical.Artifacts[0], uncanonical.Artifacts[1] = uncanonical.Artifacts[1], uncanonical.Artifacts[0]
	require.ErrorContains(t, runnable.VerifyPackage(uncanonical), "not in canonical form")
}

func TestPreparePackageRejectsIncompleteDescriptors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(pkg *basev0.RunnablePackage)
		want   string
	}{
		{"unknown schema", func(p *basev0.RunnablePackage) { p.Schema = "codefly.runnable-package/v2" }, "schema \"codefly.runnable-package/v2\" is not supported"},
		{"loose version", func(p *basev0.RunnablePackage) { p.Identity.Version = "1.0.0.0" }, "not a strict semantic version"},
		{"service agent", func(p *basev0.RunnablePackage) { p.Agent.Kind = basev0.Agent_SERVICE }, "agent kind codefly:service is not codefly:runnable"},
		{"unpinned agent", func(p *basev0.RunnablePackage) { p.Agent.Version = "latest" }, "agent version must be pinned"},
		{"no contract", func(p *basev0.RunnablePackage) { p.Contract = nil }, "contract"},
		{"unknown protocol", func(p *basev0.RunnablePackage) { p.Contract.Protocol = "other/v9" }, "protocol \"other/v9\" is not supported"},
		{"enum-like type", func(p *basev0.RunnablePackage) { p.Contract.Input.Fields[0].Type = basev0.RunnableField_UNKNOWN }, "outside the bounded profile"},
		{"no facilities", func(p *basev0.RunnablePackage) { p.Execution.Facilities = nil }, "facilities"},
		{"unknown facility", func(p *basev0.RunnablePackage) { p.Execution.Facilities[0].Kind = basev0.RunnableFacility_UNKNOWN }, "facility UNKNOWN is not supported"},
		{"duplicate facility", func(p *basev0.RunnablePackage) { p.Execution.Facilities[1].Kind = basev0.RunnableFacility_KUBERNETES }, "declared twice"},
		{"no timeout", func(p *basev0.RunnablePackage) { p.Execution.Timeout = nil }, "timeout"},
		{"zero timeout", func(p *basev0.RunnablePackage) { p.Execution.Timeout = durationpb.New(0) }, "timeout must be positive"},
		{"no cancellation", func(p *basev0.RunnablePackage) {
			p.Execution.Cancellation = basev0.RunnableExecution_CANCELLATION_UNKNOWN
		}, "cancellation is required"},
		{"no recovery", func(p *basev0.RunnablePackage) { p.Execution.Recovery = basev0.RunnableExecution_RECOVERY_UNKNOWN }, "recovery is required"},
		{"zero payload bound", func(p *basev0.RunnablePackage) { p.Execution.MaxInputBytes = 0 }, "max_input_bytes"},
		{"no build", func(p *basev0.RunnablePackage) { p.Build = nil }, "build"},
		{"unpinned handler", func(p *basev0.RunnablePackage) { p.Build.Handler.Digest = "sha256:short" }, "digest"},
		{"duplicate input", func(p *basev0.RunnablePackage) { p.Build.Inputs[1].Path = "uv.lock" }, "pinned twice"},
		{"no toolchain", func(p *basev0.RunnablePackage) { p.Build.Toolchain = "" }, "toolchain"},
		{"no artifacts", func(p *basev0.RunnablePackage) { p.Artifacts = nil }, "artifacts"},
		{"bad platform", func(p *basev0.RunnablePackage) { p.Artifacts[0].Platform = "linux" }, "must be os/arch"},
		{"native without command", func(p *basev0.RunnablePackage) { p.Artifacts[1].Command = nil }, "declares no launch command"},
		{"image with command", func(p *basev0.RunnablePackage) { p.Artifacts[0].Command = []string{"python"} }, "must not declare a launch command"},
		{"unpinned image", func(p *basev0.RunnablePackage) { p.Artifacts[0].Reference = "ghcr.io/example/word-count:0.1.0" }, "not pinned to its digest"},
		{"duplicate artifact", func(p *basev0.RunnablePackage) {
			p.Artifacts[1] = proto.Clone(p.Artifacts[0]).(*basev0.RunnableArtifact)
		}, "declared twice"},
		{"artifact without facility", func(p *basev0.RunnablePackage) {
			p.Execution.Facilities = p.Execution.Facilities[1:]
		}, "needs facility KUBERNETES, which the package does not declare"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg := samplePackage(t)
			tc.mutate(pkg)
			_, err := runnable.PreparePackage(pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}
	_, err := runnable.PreparePackage(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestCompareReleaseIsIdempotentAndConflictsOnChangedContent(t *testing.T) {
	existing := preparedPackage(t)
	identical := preparedPackage(t)
	require.NoError(t, runnable.CompareRelease(existing, identical))

	changed := samplePackage(t)
	changed.Build.Handler.Digest = digestC
	rebuilt, err := runnable.PreparePackage(changed)
	require.NoError(t, err)
	err = runnable.CompareRelease(existing, rebuilt)
	require.ErrorIs(t, err, runnable.ErrConflict)
	require.ErrorContains(t, err, "with-runnables/word-count@0.1.0 is already registered")

	// A new release coexists: it is not a conflict, and not the same release.
	next := samplePackage(t)
	next.Identity.Version = "0.2.0"
	nextRelease, err := runnable.PreparePackage(next)
	require.NoError(t, err)
	err = runnable.CompareRelease(existing, nextRelease)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.NotErrorIs(t, err, runnable.ErrConflict)

	require.ErrorContains(t, runnable.CompareRelease(samplePackage(t), identical), "existing")

	// Bytes outside the declared schema cannot be silently absorbed into an
	// identity this version computes.
	foreign := samplePackage(t)
	foreign.ProtoReflect().SetUnknown([]byte{0xf8, 0x7f, 0x01})
	_, err = runnable.PreparePackage(foreign)
	require.ErrorContains(t, err, "outside its declared schema")
}

func sampleBinding(pkg *basev0.RunnablePackage, artifact *basev0.RunnableArtifact, facility basev0.RunnableFacility_Kind) *basev0.RunnableBinding {
	return &basev0.RunnableBinding{
		Schema:        runnable.BindingSchemaV1,
		Identity:      proto.Clone(pkg.GetIdentity()).(*basev0.RunnableIdentity),
		PackageDigest: pkg.GetDigest(),
		Facility:      &basev0.RunnableFacility{Kind: facility},
		Artifact:      proto.Clone(artifact).(*basev0.RunnableArtifact),
		DependencyNetworkMappings: []*basev0.NetworkMapping{{
			Endpoint: &basev0.Endpoint{Name: "tcp", Service: "store", Module: "with-runnables", Api: "tcp", Visibility: "module"},
			Instances: []*basev0.NetworkInstance{
				{Host: "store-1.with-runnables.svc", Port: 5432, Address: "store-1.with-runnables.svc:5432"},
				{Host: "store-0.with-runnables.svc", Port: 5432, Address: "store-0.with-runnables.svc:5432"},
			},
		}},
		CredentialReferences:    []string{"store/password", "artifact-store/token"},
		ConfigurationReferences: []string{"openai", "artifact-store"},
	}
}

func TestPrepareBindingPinsPackageFacilityAndArtifact(t *testing.T) {
	pkg := preparedPackage(t)
	native, image := pkg.GetArtifacts()[0], pkg.GetArtifacts()[1]

	binding, err := runnable.PrepareBinding(sampleBinding(pkg, image, basev0.RunnableFacility_KUBERNETES), pkg)
	require.NoError(t, err)
	require.Len(t, binding.GetDigest(), 64)
	require.Equal(t, []string{"artifact-store/token", "store/password"}, binding.GetCredentialReferences())
	require.Equal(t, []string{"artifact-store", "openai"}, binding.GetConfigurationReferences())
	require.Equal(t, "store-0.with-runnables.svc:5432", binding.GetDependencyNetworkMappings()[0].GetInstances()[0].GetAddress())
	require.NoError(t, runnable.VerifyBinding(binding, pkg))

	// Mappings and instances are sets: an installer that emits them in
	// another order has installed the same thing.
	shuffled := sampleBinding(pkg, image, basev0.RunnableFacility_KUBERNETES)
	shuffled.DependencyNetworkMappings = append(shuffled.DependencyNetworkMappings, &basev0.NetworkMapping{
		Endpoint: &basev0.Endpoint{Name: "admin", Service: "store", Module: "with-runnables", Api: "http", Visibility: "module"},
	})
	ordered := sampleBinding(pkg, image, basev0.RunnableFacility_KUBERNETES)
	ordered.DependencyNetworkMappings = append([]*basev0.NetworkMapping{proto.Clone(shuffled.DependencyNetworkMappings[1]).(*basev0.NetworkMapping)}, ordered.DependencyNetworkMappings...)
	ordered.DependencyNetworkMappings[1].Instances[0], ordered.DependencyNetworkMappings[1].Instances[1] = ordered.DependencyNetworkMappings[1].Instances[1], ordered.DependencyNetworkMappings[1].Instances[0]
	withoutEndpointPin := samplePackage(t)
	withoutEndpointPin.ServiceDependencies[0].Endpoints = nil
	anyEndpoint, err := runnable.PreparePackage(withoutEndpointPin)
	require.NoError(t, err)
	shuffled.PackageDigest, ordered.PackageDigest = anyEndpoint.GetDigest(), anyEndpoint.GetDigest()
	shuffledPrepared, err := runnable.PrepareBinding(shuffled, anyEndpoint)
	require.NoError(t, err)
	orderedPrepared, err := runnable.PrepareBinding(ordered, anyEndpoint)
	require.NoError(t, err)
	require.Equal(t, shuffledPrepared.GetDigest(), orderedPrepared.GetDigest())

	nativeBinding, err := runnable.PrepareBinding(sampleBinding(pkg, native, basev0.RunnableFacility_NATIVE), pkg)
	require.NoError(t, err)
	require.NotEqual(t, binding.GetDigest(), nativeBinding.GetDigest())

	// A binding is tied to the exact package bytes it installed: a rebuilt
	// release with the same identity does not verify against it.
	changed := samplePackage(t)
	changed.Build.Handler.Digest = digestC
	rebuilt, err := runnable.PreparePackage(changed)
	require.NoError(t, err)
	err = runnable.VerifyBinding(binding, rebuilt)
	require.ErrorContains(t, err, "installs package digest")

	require.ErrorContains(t, runnable.VerifyBinding(sampleBinding(pkg, image, basev0.RunnableFacility_KUBERNETES), pkg), "carries no digest")
	_, err = runnable.PrepareBinding(binding, samplePackage(t))
	require.ErrorContains(t, err, "carries no digest")

	cases := []struct {
		name   string
		mutate func(b *basev0.RunnableBinding)
		want   string
	}{
		{"unknown schema", func(b *basev0.RunnableBinding) { b.Schema = "codefly.runnable-binding/v0" }, "not supported"},
		{"other release", func(b *basev0.RunnableBinding) { b.Identity.Version = "0.2.0" }, "does not match the package release identity"},
		{"native artifact on kubernetes", func(b *basev0.RunnableBinding) { b.Artifact = proto.Clone(native).(*basev0.RunnableArtifact) }, "NATIVE artifact cannot execute on facility KUBERNETES"},
		{"foreign artifact", func(b *basev0.RunnableBinding) { b.Artifact.Digest = digestC }, "not one of the package artifacts"},
		{"undeclared dependency", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Endpoint.Service = "cache" }, "does not declare as a dependency"},
		{"unconsumed endpoint", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Endpoint.Name = "admin" }, "does not consume"},
		{"missing configuration", func(b *basev0.RunnableBinding) { b.ConfigurationReferences = []string{"openai"} }, "but the package declares"},
		{"extra configuration", func(b *basev0.RunnableBinding) {
			b.ConfigurationReferences = append(b.ConfigurationReferences, "stripe")
		}, "but the package declares"},
		{"duplicate configuration", func(b *basev0.RunnableBinding) {
			b.ConfigurationReferences = []string{"openai", "openai", "artifact-store"}
		}, "declared twice"},
		{"empty credential reference", func(b *basev0.RunnableBinding) { b.CredentialReferences = []string{" "} }, "cannot be empty"},
		{"duplicate credential reference", func(b *basev0.RunnableBinding) { b.CredentialReferences = []string{"a", "a"} }, "declared twice"},
		{"wrong digest", func(b *basev0.RunnableBinding) { b.Digest = strings.Repeat("f", 64) }, "does not match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBinding(pkg, image, basev0.RunnableFacility_KUBERNETES)
			tc.mutate(b)
			_, err := runnable.PrepareBinding(b, pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}

	nativeOnly := samplePackage(t)
	nativeOnly.Execution.Facilities = nativeOnly.Execution.Facilities[1:]
	nativeOnly.Artifacts = nativeOnly.Artifacts[1:]
	nativeOnlyPkg, err := runnable.PreparePackage(nativeOnly)
	require.NoError(t, err)
	_, err = runnable.PrepareBinding(sampleBinding(nativeOnlyPkg, image, basev0.RunnableFacility_KUBERNETES), nativeOnlyPkg)
	require.ErrorContains(t, err, "does not allow execution facility KUBERNETES")
}
