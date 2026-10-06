package ciguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A job that receives a credential must match a REGISTERED TEMPLATE exactly, or
// it is refused.
//
// Six review rounds analysed what jobs do: which contexts are a source, which
// fields are a sink, which commands execute a value, which conditions skip a
// refusal. Each round closed the routes it named and the next found more --
// `$GITHUB_OUTPUT`, `$GITHUB_EVENT_PATH`, `toJSON(github)`, `NODE_OPTIONS`,
// `working-directory`, `git restore --source`, `git archive | tar -x`, a
// composite step reusing the refusal's name. The list of things a workflow can
// do is not a list this package can finish, and a guard built from one is
// always one construction behind.
//
// So nothing is analysed. The question is not "what does this job do" but "is
// this job one of the shapes this repository runs". The answer is a digest: a
// template records the job exactly as written, and a job that differs in any
// respect -- a step added, an input changed, a condition appearing -- does not
// match and is refused. A new source, sink or verb is then not something to
// model; it changes the shape, and the shape is pinned.
//
// Changing one of these jobs therefore means updating its template and the test
// that executes it, deliberately, in the same change. That friction is the
// point: these five jobs are the only ones in this repository that receive a
// secret or a write token.

// jobTemplate is one permitted shape.
type jobTemplate struct {
	workflow, job string
	// digest is the SHA-256 of the job's canonical YAML. It pins every field,
	// including the ones no rule here would have thought to read.
	digest string
	// executedBy is the test that runs this job's own script against real
	// repositories, where it has one. A template whose behaviour nothing
	// executes is accepted only for a job that runs no script of its own.
	executedBy string
	// why records what this job is for, so a reviewer reading a digest change
	// knows what they are being asked to approve.
	why string
}

// permittedCredentialJobs is the allowlist. Nothing else may hold a credential.
var permittedCredentialJobs = []jobTemplate{
	{
		workflow: "version-tag.yml", job: "tag",
		executedBy: "TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch",
		why:        "cuts the release tag from version/info.codefly.yaml on a commit proven to be on the default branch",
	},
	{
		workflow: "go-service-release.yml", job: "goreleaser",
		executedBy: "TestAReleaseIsAdmittedOnlyFromTheRepositorysOwnDefaultBranch",
		why:        "publishes a release for a tag proven to be on the repository's own default branch",
	},
	{
		workflow: "combine-deps.yml", job: "publish",
		executedBy: "TestTheCombinedBranchTravelsAsABundleWithoutBeingCheckedOut",
		why:        "pushes the branch the unprivileged plan job assembled, without checking it out",
	},
	{
		workflow: "go.yml", job: "coverage-badge",
		why: "pushes the coverage badge README.md renders; runs no script of its own",
	},
	{
		workflow: "go.yml", job: "notify",
		why: "posts the build result to Slack; no checkout, no script",
	},
}

// canonicalJobDigest renders a job to canonical YAML and hashes it, so the
// digest depends on the job's content and not on its formatting.
func canonicalJobDigest(t *testing.T, node yaml.Node) string {
	t.Helper()

	var canonical any
	require.NoError(t, node.Decode(&canonical))
	rendered, err := yaml.Marshal(canonical)
	require.NoError(t, err)
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:])
}

// templateFor returns the registered template for a job, if there is one.
func templateFor(workflow, job string) (jobTemplate, bool) {
	for _, candidate := range permittedCredentialJobs {
		if candidate.workflow == workflow && candidate.job == job {
			return candidate, true
		}
	}
	return jobTemplate{}, false
}

// credentialJobIsAccepted is the whole decision: the job is one of the
// permitted shapes, unchanged, and its behaviour is executed by a test.
func credentialJobIsAccepted(t *testing.T, wf isolatedWorkflow, id string) (bool, string) {
	t.Helper()

	template, registered := templateFor(wf.name, id)
	if !registered {
		return false, "it is not one of the job shapes this repository runs with a " +
			"credential. Those are registered in permittedCredentialJobs, each with a " +
			"test that executes it; a job that is not one of them is refused rather " +
			"than analysed, because the set of things a workflow can do to reach " +
			"unreviewed code is not a set this package can enumerate"
	}

	raw, ok := wf.raw[id]
	require.True(t, ok, "job %q was not captured verbatim", id)
	digest := canonicalJobDigest(t, raw)
	if template.digest != "" && template.digest != digest {
		return false, "its shape has changed. The registered template for " +
			template.workflow + "/" + template.job + " (" + template.why + ") is " +
			template.digest[:12] + " and this job is " + digest[:12] + ". Update the " +
			"template and the test that executes it in the same change, so the new " +
			"shape is approved rather than assumed"
	}
	return true, ""
}

// The digests are recorded here rather than in the template literals so that
// updating one is a visible, reviewable line. A template with no digest yet
// fails this test with the digest to paste in.
// recordedDigests is the approved shape of each permitted job. A change to one
// of these jobs changes its digest, and the guard then refuses the job until
// this line is updated -- which is the review step: somebody has to look at
// what changed and approve it, rather than the change being inherited.
var recordedDigests = map[string]string{
	"combine-deps.yml/publish":          "b16775ce2f7483b3a53f9fac309f8fa134dbc1ce9f614fadd2f09dc9d9221544",
	"go-service-release.yml/goreleaser": "f4e09a91c416c6e8ea1cd214b3742b8ce46627b9dcacfe50584391f6bb32ba03",
	"go.yml/coverage-badge":             "1930ae41205e021a17e03947d6822a6bbf0a1146190bbaf9dba19c279a1bc510",
	"go.yml/notify":                     "2c89cd13ad6d21a8affc6cb4612c169d59de8945f4592b5fdc2672dd1e153187",
	"version-tag.yml/tag":               "d6e0d6228121aaabf1e8359ebdcd4b2a4a8fdf57375548ae41dd3d7b8f933d76",
}

// TestEveryPermittedJobMatchesItsRecordedShape is the gate: each permitted job
// must still be the shape that was approved, and each template that claims an
// executing test must name one that exists.
func TestEveryPermittedJobMatchesItsRecordedShape(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)

	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "ciguard", "*_test.go"))
	require.NoError(t, err)
	var all strings.Builder
	for _, source := range sources {
		body, err := os.ReadFile(source)
		require.NoError(t, err)
		all.Write(body)
	}
	tests := all.String()

	for _, template := range permittedCredentialJobs {
		t.Run(template.workflow+"/"+template.job, func(t *testing.T) {
			wf, ok := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
			require.True(t, ok, "%s does not exist", template.workflow)
			raw, ok := wf.raw[template.job]
			require.True(t, ok, "%s has no job %q", template.workflow, template.job)

			digest := canonicalJobDigest(t, raw)
			recorded, present := recordedDigests[template.workflow+"/"+template.job]
			require.True(t, present,
				"no shape recorded for %s/%s. Add this line to recordedDigests:\n"+
					"\t%q: %q,", template.workflow, template.job,
				template.workflow+"/"+template.job, digest)
			require.Equal(t, recorded, digest,
				"%s/%s has changed shape. It is %s (%s). If the change is intended, "+
					"update recordedDigests and the test that executes this job, so the "+
					"new shape is approved rather than inherited.",
				template.workflow, template.job, template.why, digest)

			if template.executedBy != "" {
				require.Contains(t, tests, "func "+template.executedBy+"(",
					"%s/%s names %s as executing it, and no such test exists",
					template.workflow, template.job, template.executedBy)
			}
		})
	}
}

// And the converse, which is what makes this an allowlist: every job that
// receives a credential is registered. A new one is refused until someone adds
// a template and a test for it.
func TestEveryCredentialBearingJobIsRegistered(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	found := 0
	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			holdsSecret := len(secretsIn(t, wf, id)) > 0
			permissioned := parsePermissionedFile(t, path)
			holdsWrite := writeCapable(effectivePermissions(permissioned, permissioned.Jobs[id]))
			if !holdsSecret && !holdsWrite {
				continue
			}
			found++
			accepted, missing := credentialJobIsAccepted(t, wf, id)
			require.True(t, accepted, "%s: job %q %s", filepath.Base(path), id, missing)
		}
	}
	require.NotZero(t, found, "no credential-bearing job found, so this proves nothing")

	// The allowlist carries no entry for a job that does not exist.
	for _, template := range permittedCredentialJobs {
		wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
		_, ok := wf.Jobs[template.job]
		require.True(t, ok,
			"permittedCredentialJobs lists %s/%s, which does not exist",
			template.workflow, template.job)
	}
	require.Len(t, permittedCredentialJobs, found,
		"the allowlist and the set of credential-bearing jobs must be the same size, "+
			"or one of them is carrying an entry the other does not")
}

// parsePermissionedFile reads one workflow for its permission blocks.
func parsePermissionedFile(t *testing.T, path string) permissionedWorkflow {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var wf permissionedWorkflow
	require.NoError(t, yaml.Unmarshal(raw, &wf), path)
	return wf
}

// sortedTemplateNames is used by the message above and keeps the output stable.
func sortedTemplateNames() []string {
	out := make([]string, 0, len(permittedCredentialJobs))
	for _, template := range permittedCredentialJobs {
		out = append(out, template.workflow+"/"+template.job)
	}
	sort.Strings(out)
	return out
}
