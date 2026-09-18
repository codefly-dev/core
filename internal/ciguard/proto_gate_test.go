package ciguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The proto breaking-change gate in go.yml runs `buf breaking` against main.
// Which rules it applies, and which buf runs them, are configuration no other
// test reads — and both were wrong when the gate first landed.

// proto/buf.yaml must select PACKAGE. With no `breaking:` block at all — how
// the gate shipped — buf falls back to its FILE default, whose per-file
// deletion rules (MESSAGE_NO_DELETE, ENUM_NO_DELETE) reject moving a message
// to another file in the same package. Consumers reach these schemas over the
// wire or through the generated Go package, and neither can observe which
// .proto file a message is declared in, so FILE blocks refactors that break
// nothing: relocating enum StorageAuthorityKind to a new file builds clean yet
// exits 100 with "was deleted from file".
//
// PACKAGE still blocks what matters — a renamed enum value, a changed field
// type, and a message deleted from the package all exit 100 under it.
func TestProtoModuleUsesPackageBreakingRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "proto", "buf.yaml"))
	require.NoError(t, err)

	var cfg struct {
		Breaking struct {
			Use []string `yaml:"use"`
		} `yaml:"breaking"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cfg))

	require.Equal(t, []string{"PACKAGE"}, cfg.Breaking.Use,
		"proto/buf.yaml must select the PACKAGE breaking rules. Absent, buf applies "+
			"its FILE default, which fails a message moved between files in the same "+
			"package even though the wire and the generated Go symbol are identical.")
}

var bakedBufPin = regexp.MustCompile(`github\.com/bufbuild/buf/cmd/buf@v([0-9]+\.[0-9]+\.[0-9]+)`)

var makefileBufPin = regexp.MustCompile(`(?m)^BUF_VERSION\s*[:?]?=\s*([0-9]+\.[0-9]+\.[0-9]+)`)

// CI's buf, the buf the proto companion bakes, and the buf the Makefile targets
// run must all be the same one. The companion generates every consumer's
// bindings; CI decides whether a schema change is allowed to land; the Makefile
// is what a developer runs before pushing. Run them on different versions and
// the gate can pass a schema the generator then chokes on, or a local check can
// report clean on a buf that is not the one judging the change.
//
// Nothing keeps these together on its own: buf-setup-action's `version` input
// defaults to the action's own release rather than the latest, and Dependabot
// moves the SHA pin above it but never the input — so the version means
// whatever it meant the day someone typed it. CI shipped 1.73.0 against the
// companion's 1.71.0 for exactly that reason, and the Makefile pin joined this
// check because the docs used to prescribe a bare `buf`, which answered from
// whatever was on PATH.
func TestWorkflowBufMatchesTheCompanionImage(t *testing.T) {
	root := repoRoot(t)

	dockerfile, err := os.ReadFile(filepath.Join(root, "companions", "proto", "Dockerfile"))
	require.NoError(t, err)
	baked := bakedBufPin.FindSubmatch(dockerfile)
	require.NotNil(t, baked,
		"companions/proto/Dockerfile no longer pins github.com/bufbuild/buf/cmd/buf "+
			"to an explicit version, so there is nothing for CI to match")
	want := string(baked[1])

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)
	pinned := makefileBufPin.FindSubmatch(makefile)
	require.NotNil(t, pinned,
		"the Makefile no longer declares BUF_VERSION, so `make buf-lint` and "+
			"`make buf-breaking` cannot be pinned to the buf CI and the companion run")
	require.Equal(t, want, string(pinned[1]),
		"the Makefile pins buf %s but companions/proto/Dockerfile bakes %s. A local "+
			"`make buf-breaking` would then answer from a different buf than the gate "+
			"that decides whether the schema lands.",
		string(pinned[1]), want)

	installed := 0
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf workflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				if !strings.Contains(step.Uses, "bufbuild/buf-setup-action") {
					continue
				}
				version, ok := step.With["version"]
				require.True(t, ok,
					"%s: job %q installs buf without pinning `version`; the action then "+
						"installs its own default release, which is not the latest and not "+
						"what the companion bakes.",
					filepath.Base(path), jobName)
				installed++
				require.Equal(t, want, fmt.Sprint(version),
					"%s: job %q installs buf %v but companions/proto/Dockerfile bakes %s. "+
						"The gate and the generator have to run the same buf, or CI can pass "+
						"a schema the companion then fails to generate.",
					filepath.Base(path), jobName, version, want)
			}
		}
	}
	require.NotZero(t, installed,
		"no workflow installs buf, so the proto breaking-change gate cannot run")
}
