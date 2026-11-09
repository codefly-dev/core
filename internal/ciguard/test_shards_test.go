package ciguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// go.yml runs the suite once, split by package across a matrix of shards that
// each ask scripts/test_shard.sh for their slice. Nothing else notices a
// package that falls between the slices: every shard goes green, coverage
// merges what it was given, and the package's tests have silently stopped
// running. So the partition is proved here, against the workflow's own count
// and tags.
func TestTestShardsPartitionEveryPackage(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "go.yml"))
	require.NoError(t, err)

	var wf struct {
		Jobs map[string]struct {
			Env      map[string]string `yaml:"env"`
			Strategy struct {
				Matrix yaml.Node `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &wf))
	job, ok := wf.Jobs["test"]
	require.True(t, ok, "go.yml has no `test` job; this guard and the shard script describe one")

	total, err := strconv.Atoi(job.Env["SHARDS"])
	require.NoError(t, err, "go.yml's test job must set SHARDS to the matrix size")
	want := make([]int, total)
	for i := range want {
		want[i] = i + 1
	}
	// The matrix GitHub expands, not one axis of it: an `include:` or
	// `exclude:` beside `shard:` changes which shards run, and a shard that
	// never runs takes its packages with it.
	require.Equal(t, []string{"shard"}, mappingKeys(&job.Strategy.Matrix),
		"the test matrix must be the one literal `shard:` axis, with no include/exclude")
	var shards []int
	require.NoError(t, job.Strategy.Matrix.Content[1].Decode(&shards))
	require.Equal(t, want, shards,
		"the shard matrix must be exactly 1..SHARDS, or a slice of packages has no job running it")

	// Coverage refuses a merge missing any shard's profile, by the same count.
	coverage, ok := wf.Jobs["coverage"]
	require.True(t, ok, "go.yml has no `coverage` job to merge the shards' profiles")
	require.Equal(t, job.Env["SHARDS"], coverage.Env["SHARDS"],
		"the coverage job must expect exactly as many profiles as there are shards")
	refuses := false
	for _, step := range coverage.Steps {
		if strings.Contains(step.Run, `"${#profiles[@]}" -ne "$SHARDS"`) {
			refuses = true
		}
	}
	require.True(t, refuses, "the coverage merge no longer refuses a missing shard profile")

	// The shards list packages under the tags they test with; a package that
	// exists only under a tag must be in a slice too.
	const tags = "sandbox_e2e,nix_required"
	runs := false
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "./scripts/test_shard.sh") {
			runs = true
			require.Contains(t, step.Run, "-tags="+tags,
				"the shards list packages under %s (scripts/test_shard.sh) but test under other tags", tags)
		}
	}
	require.True(t, runs, "no step of go.yml's test job asks scripts/test_shard.sh for its packages")

	list := exec.Command("go", "list", "-tags", tags, "./...")
	list.Dir = root
	out, err := list.Output()
	require.NoError(t, err)
	all := strings.Fields(string(out))
	require.NotEmpty(t, all)

	seen := map[string]int{}
	for shard := 1; shard <= total; shard++ {
		cmd := exec.Command("bash", filepath.Join(root, "scripts", "test_shard.sh"), strconv.Itoa(shard), strconv.Itoa(total))
		cmd.Dir = root
		got, err := cmd.Output()
		require.NoError(t, err, "shard %d", shard)
		pkgs := strings.Fields(string(got))
		require.NotEmpty(t, pkgs, "shard %d runs no package", shard)
		for _, pkg := range pkgs {
			seen[pkg]++
		}
	}

	var missing, twice []string
	for _, pkg := range all {
		switch seen[pkg] {
		case 0:
			missing = append(missing, pkg)
		case 1:
		default:
			twice = append(twice, pkg)
		}
		delete(seen, pkg)
	}
	var unknown []string
	for pkg := range seen {
		unknown = append(unknown, pkg)
	}
	sort.Strings(unknown)
	require.Empty(t, missing, "packages no shard tests")
	require.Empty(t, twice, "packages more than one shard tests (their coverage blocks would be counted twice)")
	require.Empty(t, unknown, "shards list packages go list does not")
}
