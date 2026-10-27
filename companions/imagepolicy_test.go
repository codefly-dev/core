package companions_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions"
	"github.com/codefly-dev/core/imagepolicy"
	"github.com/stretchr/testify/require"
)

// unpinnedUpstreamBases are the companion bases that still float on a tag.
//
// Every entry is a build whose inputs change under it without anything saying
// so: the tag moves, the next build is different bytes, and a scan of the
// previous one described an image that no longer exists. These are build-time
// images rather than deployed ones, which is why they were never caught — but
// they are what compiles and generates user code.
//
// The list exists so the invariant can be turned on NOW, before the pinning is
// done, instead of waiting for it. Adding an entry is a deliberate, reviewed
// decision and the only correct direction is down. Pinning one means deleting
// its line: TestEveryAllowedUnpinnedBaseIsStillUnpinned fails on an entry that
// no longer applies, so the list cannot quietly outlive the debt.
//
// Pinning these is not a drive-by edit. Companion images are a separate release
// contract — built from their own manifest and published by the CLI, not by the
// core release flow — so moving a base means republishing the companion. See
// docs/runbooks/publish-companions.md.
var unpinnedUpstreamBases = []string{
	"alpine:3.24.2",
	"docker:29-dind",
	"golang:1.27-alpine",
	"node:26.3.0-alpine3.22",
	"python:3.14.1-alpine3.21",
	"rust:1.99-alpine",
}

// corePolicy is what core requires of its own images. It is an ordinary
// imagepolicy.Policy, the same shape a deployment target declares, so this test
// also demonstrates the package against real Dockerfiles rather than fixtures.
//
// No registry allow-list: where a companion is published is the builder's
// decision, and TestRegistryLiteralsAreConfinedToTheBaseImageDefault already
// owns the one reference core still carries. No required labels: a companion is
// not deployed into a customer's cluster, so a compliance programme's labels do
// not apply to it.
var corePolicy = imagepolicy.Policy{RequireDigest: true}

// refusals runs the policy over one companion and drops the base-image
// argument's default.
//
// That default is a tag on purpose: the publisher overrides it with the digest
// it just pushed, so the tag in the file is not a claim about what gets built.
// The same carve-out, for the same reason, as the registry-literal test.
func refusals(t *testing.T, spec companions.BuildSpec) []imagepolicy.Refusal {
	t.Helper()
	content := dockerfile(t, spec)
	baseDefault := ""
	for _, instruction := range companions.Instructions(content) {
		text := strings.TrimSpace(instruction.Text)
		if prefix := "ARG " + companions.BaseImageArg + "="; strings.HasPrefix(text, prefix) {
			baseDefault = strings.Trim(strings.TrimPrefix(text, prefix), `"'`)
		}
	}
	var kept []imagepolicy.Refusal
	for _, refusal := range imagepolicy.Check(content, corePolicy) {
		if spec.Base != "" && baseDefault != "" && refusal.Detail == baseDefault && errors.Is(refusal, imagepolicy.ErrUnpinnedBase) {
			continue
		}
		kept = append(kept, refusal)
	}
	return kept
}

// These files ship scratch-based builders and the publish action's smoke image.
// Apply the same policy so replacing scratch with a floating base fails here.
func TestCoreImageTemplatesSatisfyPolicy(t *testing.T) {
	for _, path := range []string{
		"runners/golang/templates/builder/Dockerfile.tmpl",
		"runners/rust/templates/builder/Dockerfile.tmpl",
		".github/actions/publish-service-image/testdata/Dockerfile",
	} {
		t.Run(path, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(repositoryRoot(t), path))
			require.NoError(t, err)
			require.Empty(t, imagepolicy.Check(string(content), corePolicy))
		})
	}
}

// A companion base that is neither pinned nor a known debt entry is a new
// floating input, and it fails here rather than in whatever build first
// notices the tag moved.
func TestNoNewUnpinnedCompanionBase(t *testing.T) {
	allowed := make(map[string]bool, len(unpinnedUpstreamBases))
	for _, base := range unpinnedUpstreamBases {
		allowed[base] = true
	}
	for _, spec := range buildSpecs(t) {
		for _, refusal := range refusals(t, spec) {
			// The debt list excuses a floating tag and nothing else: any other
			// rule failing is a finding whatever the reference is.
			if errors.Is(refusal, imagepolicy.ErrUnpinnedBase) && allowed[refusal.Detail] {
				continue
			}
			require.Failf(t, "companion image policy",
				"%s:%d %v. Pin the base by digest; add it to unpinnedUpstreamBases "+
					"only as a reviewed exception.",
				spec.Dockerfile, refusal.Line, refusal)
		}
	}
}

// The debt list shrinks or it rots. An entry that no longer appears unpinned in
// any companion has been pinned (or removed), and leaving it behind would let
// the next floating base of the same name back in unnoticed.
func TestEveryAllowedUnpinnedBaseIsStillUnpinned(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range buildSpecs(t) {
		for _, refusal := range refusals(t, spec) {
			seen[refusal.Detail] = true
		}
	}
	for _, base := range unpinnedUpstreamBases {
		require.Truef(t, seen[base],
			"unpinnedUpstreamBases lists %q, but no companion names it unpinned any more. "+
				"Delete the entry in the change that pinned it.", base)
	}
}
