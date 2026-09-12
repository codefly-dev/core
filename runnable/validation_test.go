package runnable_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

func TestPreparePackageRejectsIncompleteWireContracts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*basev0.RunnablePackage)
	}{
		{"missing input", func(p *basev0.RunnablePackage) { p.Contract.Input = nil }},
		{"missing output", func(p *basev0.RunnablePackage) { p.Contract.Output = nil }},
		{"future recovery", func(p *basev0.RunnablePackage) { p.Execution.Recovery = 99 }},
		{"negative recovery", func(p *basev0.RunnablePackage) { p.Execution.Recovery = -1 }},
		{"future cancellation", func(p *basev0.RunnablePackage) { p.Execution.Cancellation = 99 }},
		{"negative cancellation", func(p *basev0.RunnablePackage) { p.Execution.Cancellation = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := samplePackage(t)
			tc.mutate(p)
			_, err := runnable.PreparePackage(p)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}
}

func TestPrepareBindingRejectsUnresolvedEndpoints(t *testing.T) {
	p := preparedPackage(t)
	for _, tc := range []struct {
		name   string
		mutate func(*basev0.RunnableBinding)
		want   string
	}{
		{"missing mapping", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings = nil }, "does not resolve dependency"},
		{"nil mapping", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0] = nil }, "requires an endpoint"},
		{"missing endpoint", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Endpoint = nil }, "requires an endpoint"},
		{"missing instances", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Instances = nil }, "requires a network instance"},
		{"nil instance", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Instances[0] = nil }, "nonempty instance address"},
		{"empty instance", func(b *basev0.RunnableBinding) {
			b.DependencyNetworkMappings[0].Instances[0] = &basev0.NetworkInstance{}
		}, "nonempty instance address"},
		{"blank address", func(b *basev0.RunnableBinding) { b.DependencyNetworkMappings[0].Instances[0].Address = " " }, "nonempty instance address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBinding(p, p.Artifacts[0], basev0.RunnableFacility_NATIVE)
			tc.mutate(b)
			_, err := runnable.PrepareBinding(b, p)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("partial endpoint selection", func(t *testing.T) {
		p := samplePackage(t)
		p.ServiceDependencies[0].Endpoints = []string{"tcp", "admin"}
		pkg, err := runnable.PreparePackage(p)
		require.NoError(t, err)
		b := sampleBinding(pkg, pkg.Artifacts[0], basev0.RunnableFacility_NATIVE)
		_, err = runnable.PrepareBinding(b, pkg)
		require.ErrorContains(t, err, "does not resolve endpoint with-runnables/store/admin")
	})

	t.Run("runtime all endpoints still needs a mapping", func(t *testing.T) {
		p := samplePackage(t)
		p.ServiceDependencies[0].Endpoints = nil
		pkg, err := runnable.PreparePackage(p)
		require.NoError(t, err)
		b := sampleBinding(pkg, pkg.Artifacts[0], basev0.RunnableFacility_NATIVE)
		b.DependencyNetworkMappings = nil
		_, err = runnable.PrepareBinding(b, pkg)
		require.ErrorContains(t, err, "does not resolve dependency")
	})
}

func TestBindingRespectsDependencyKinds(t *testing.T) {
	for _, kind := range []resources.DependencyKind{
		resources.DependencyKindBuild, resources.DependencyKindSchema, resources.DependencyKindCompletion,
		resources.DependencyKindLegacy, resources.DependencyKindExternal,
	} {
		t.Run(string(kind), func(t *testing.T) {
			p := samplePackage(t)
			p.ServiceDependencies[0].Kind = string(kind)
			p.ServiceDependencies[0].Endpoints = nil
			pkg, err := runnable.PreparePackage(p)
			require.NoError(t, err)
			b := sampleBinding(pkg, pkg.Artifacts[0], basev0.RunnableFacility_NATIVE)
			b.DependencyNetworkMappings = nil
			_, err = runnable.PrepareBinding(b, pkg)
			require.NoError(t, err, "endpointless prerequisites do not require network mappings")
		})
	}
	for _, kind := range []resources.DependencyKind{resources.DependencyKindLegacy, resources.DependencyKindExternal} {
		t.Run(string(kind)+" consumes endpoints", func(t *testing.T) {
			p := samplePackage(t)
			p.ServiceDependencies[0].Kind = string(kind)
			pkg, err := runnable.PreparePackage(p)
			require.NoError(t, err)
			b := sampleBinding(pkg, pkg.Artifacts[0], basev0.RunnableFacility_NATIVE)
			b.DependencyNetworkMappings = nil
			_, err = runnable.PrepareBinding(b, pkg)
			require.ErrorContains(t, err, "does not resolve dependency")
		})
	}
}

func TestBindingRejectsAmbiguousMappingAndInstanceKeys(t *testing.T) {
	p := preparedPackage(t)
	t.Run("same endpoint with different instances", func(t *testing.T) {
		b := sampleBinding(p, p.Artifacts[0], basev0.RunnableFacility_NATIVE)
		second := proto.Clone(b.DependencyNetworkMappings[0]).(*basev0.NetworkMapping)
		b.DependencyNetworkMappings[0].Instances = b.DependencyNetworkMappings[0].Instances[:1]
		second.Instances = second.Instances[1:]
		b.DependencyNetworkMappings = append(b.DependencyNetworkMappings, second)
		for range 2 {
			_, err := runnable.PrepareBinding(b, p)
			require.ErrorContains(t, err, "maps endpoint with-runnables/store/tcp/tcp twice")
			b.DependencyNetworkMappings[0], b.DependencyNetworkMappings[1] = b.DependencyNetworkMappings[1], b.DependencyNetworkMappings[0]
		}
	})
	t.Run("same instance key with different metadata", func(t *testing.T) {
		b := sampleBinding(p, p.Artifacts[0], basev0.RunnableFacility_NATIVE)
		instances := b.DependencyNetworkMappings[0].Instances
		instances[1].Address = instances[0].Address
		for range 2 {
			_, err := runnable.PrepareBinding(b, p)
			require.ErrorContains(t, err, "repeats instance")
			instances[0], instances[1] = instances[1], instances[0]
		}
	})
	t.Run("same address with distinct access contexts is canonical", func(t *testing.T) {
		b := sampleBinding(p, p.Artifacts[0], basev0.RunnableFacility_NATIVE)
		instances := b.DependencyNetworkMappings[0].Instances
		instances[1].Address = instances[0].Address
		instances[0].Access = &basev0.NetworkAccess{Kind: "native"}
		instances[1].Access = &basev0.NetworkAccess{Kind: "container"}
		first, err := runnable.PrepareBinding(b, p)
		require.NoError(t, err)
		instances[0], instances[1] = instances[1], instances[0]
		second, err := runnable.PrepareBinding(b, p)
		require.NoError(t, err)
		require.Equal(t, first.Digest, second.Digest)
		require.NoError(t, runnable.VerifyBinding(first, p))
		require.NoError(t, runnable.VerifyBinding(second, p))
	})
}
