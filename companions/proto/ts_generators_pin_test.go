package proto

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// tsGeneratorsPinnedElsewhere lists generators the Dockerfile installs via npm
// but that are intentionally NOT pinned through ts-generators/package.json:
// protoc-gen-es is pinned from source in flake.nix and guarded by
// companion_plugins_test.go, so the reverse check below must not demand it here.
var tsGeneratorsPinnedElsewhere = map[string]bool{
	"@bufbuild/protoc-gen-es": true,
}

// TestTSGeneratorPinsInLockstep guards the invariant that keeps the two build
// backends from drifting on the non-protobuf-es TypeScript generators. The
// Dockerfile installs them with `npm install -g <pkg>@<version>`; the Nix flake
// installs the same set from ts-generators/package.json (its dependencies pin
// the versions, package-lock.json pins the closure). If someone bumps one file
// and forgets the other, the Docker image and the Nix image ship different
// generators. Assert the pinned versions match, package for package, in both
// directions. (protoc-gen-es is guarded separately by companion_plugins_test.go.)
func TestTSGeneratorPinsInLockstep(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	pkgJSON, err := os.ReadFile("ts-generators/package.json")
	if err != nil {
		t.Fatalf("read ts-generators/package.json: %v", err)
	}

	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err = json.Unmarshal(pkgJSON, &pkg); err != nil {
		t.Fatalf("parse ts-generators/package.json: %v", err)
	}
	if len(pkg.Dependencies) == 0 {
		t.Fatal("ts-generators/package.json declares no dependencies")
	}

	// Forward: every generator package.json pins must be installed at the same
	// version by the Dockerfile.
	for name, want := range pkg.Dependencies {
		if tsGeneratorsPinnedElsewhere[name] {
			t.Errorf("%q is pinned via ts-generators but is meant to be pinned from source (see companion_plugins_test.go); remove it from ts-generators/package.json", name)
			continue
		}
		re := regexp.MustCompile(regexp.QuoteMeta(name) + `@([0-9][^ \\\n]*)`)
		m := re.FindSubmatch(dockerfile)
		if m == nil {
			t.Errorf("Dockerfile does not pin %q at all (package.json wants %s); the two backends must install the same generators", name, want)
			continue
		}
		if got := string(m[1]); got != want {
			t.Errorf("version drift for %s: Dockerfile pins %s, ts-generators/package.json pins %s — bump both together (see codefly-dev/core#83)", name, got, want)
		}
	}

	// Reverse: every generator the Dockerfile installs via npm must also be
	// pinned in package.json (so the Nix image never silently omits one),
	// except those pinned elsewhere.
	npmPins := regexp.MustCompile(`(?:@[\w.-]+/)?[\w.-]+@[0-9][^ \\\n]*`).FindAllString(dockerfileNpmInstallBlock(t, dockerfile), -1)
	for _, pin := range npmPins {
		at := len(pin) - len(regexp.MustCompile(`@[0-9][^ \\\n]*$`).FindString(pin))
		name := pin[:at]
		if tsGeneratorsPinnedElsewhere[name] {
			continue
		}
		if _, ok := pkg.Dependencies[name]; !ok {
			t.Errorf("Dockerfile installs %q via npm but ts-generators/package.json does not pin it — the Nix image would omit it", name)
		}
	}
}

// dockerfileNpmInstallBlock returns just the `npm install -g ... &&` region of
// the Dockerfile, so the reverse guard doesn't pick up apk packages or other
// `name@version` strings elsewhere in the file.
func dockerfileNpmInstallBlock(t *testing.T, dockerfile []byte) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)npm install -g\b(.*?)&&`)
	m := re.FindSubmatch(dockerfile)
	if m == nil {
		t.Fatal("Dockerfile has no `npm install -g ... &&` block; the generator install layer changed shape — update this test")
	}
	return string(m[1])
}
