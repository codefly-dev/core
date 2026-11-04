package ciguard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A compiled executable must never be committed.
//
// This exists because one was. #893 added `corepindrift`, a 10MB Mach-O binary
// at the repository root: `go build ./internal/corepindrift` writes its output
// into the working directory named after the package, and a `git add -A`
// swept it into the commit. Nothing noticed — it is not a Go file, so no test
// compiled it; it is not a workflow, so no guard in this package read it; and
// `go vet`, `gofmt` and the coverage gate have nothing to say about it.
//
// The cost is not only the 10MB. A committed binary is a build output presented
// as source: it goes stale against the code the moment either changes, it is
// architecture-specific so it is wrong for most people who clone it, and it is
// the one file in the tree nobody can review.
//
// Checking the MAGIC NUMBER rather than a name list is the point. A new tool
// under internal/ gets a new binary name, and a guard listing names would pass
// it. Every executable format starts with a fixed signature, so this catches
// the class.

// executableMagic are the signatures of the formats a Go build can produce,
// plus the ar/static-library header, which is what a stray `go build -buildmode
// =archive` leaves.
var executableMagic = [][]byte{
	{0x7f, 'E', 'L', 'F'},          // ELF — Linux, BSD
	{0xfe, 0xed, 0xfa, 0xce},       // Mach-O 32-bit
	{0xfe, 0xed, 0xfa, 0xcf},       // Mach-O 64-bit
	{0xce, 0xfa, 0xed, 0xfe},       // Mach-O 32-bit, byte-swapped
	{0xcf, 0xfa, 0xed, 0xfe},       // Mach-O 64-bit, byte-swapped — what #893 committed
	{0xca, 0xfe, 0xba, 0xbe},       // Mach-O universal binary
	{'M', 'Z'},                     // PE — Windows .exe
	{'!', '<', 'a', 'r', 'c', 'h'}, // ar archive
}

// textFixtureExtensions are extensions whose files are data this repository
// deliberately ships and which can collide with a magic number by accident.
// `MZ` is only two bytes, so a text file beginning "MZ" is possible; the
// extension check keeps such a fixture from reading as an executable.
func looksLikeSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".yaml", ".yml", ".json", ".proto", ".txt", ".sh", ".py",
		".ts", ".tsx", ".js", ".sql", ".toml", ".mod", ".sum", ".info", ".tmpl":
		return true
	}
	return false
}

// trackedFiles lists what git has, so this reads the COMMITTED tree and not a
// working directory full of local build output.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	command := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := command.Output()
	require.NoError(t, err, "git ls-files failed, so this guard cannot read the committed tree")

	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	require.NotEmpty(t, files, "git ls-files returned nothing")
	return files
}

func TestNoCompiledBinaryIsCommitted(t *testing.T) {
	root := repoRoot(t)

	var found []string
	for _, name := range trackedFiles(t) {
		if looksLikeSource(name) {
			continue
		}
		// testdata holds real third-party repositories and recorded archives
		// on purpose; those are fixtures a test reads, not build output of
		// this module.
		if isFixture(name) {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		file, err := os.Open(path)
		if err != nil {
			continue
		}
		header := make([]byte, 8)
		read, _ := file.Read(header)
		_ = file.Close()
		header = header[:max(read, 0)]

		for _, magic := range executableMagic {
			if bytes.HasPrefix(header, magic) {
				found = append(found, name)
				break
			}
		}
	}

	require.Emptyf(t, found,
		"these committed files are compiled executables: %v\n\n"+
			"A build output committed as source is stale the moment either changes, "+
			"wrong for every architecture but the one that produced it, and the one "+
			"file in the tree nobody can review. `go build ./internal/<tool>` writes "+
			"its binary into the working directory named after the package, and a "+
			"`git add -A` takes it — which is exactly how #893 committed a 10MB "+
			"corepindrift.\n\n"+
			"Remove it (`git rm --cached <file>`) and add its name to .gitignore. "+
			"Run a tool with `go run ./internal/<tool>` instead, which writes no "+
			"binary into the tree.", found)
}

// The detector must actually fire, or it is a clean-tree assertion being
// believed rather than a guard. Each case is a real format signature.
func TestTheBinaryDetectorFiresOnEachExecutableFormat(t *testing.T) {
	cases := map[string][]byte{
		"ELF":                 {0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00},
		"Mach-O 64-bit":       {0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01},
		"Mach-O 32-bit":       {0xfe, 0xed, 0xfa, 0xce, 0x00, 0x00, 0x00, 0x12},
		"Mach-O universal":    {0xca, 0xfe, 0xba, 0xbe, 0x00, 0x00, 0x00, 0x02},
		"PE":                  {'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00},
		"ar archive":          {'!', '<', 'a', 'r', 'c', 'h', '>', 0x0a},
		"Mach-O byte-swapped": {0xce, 0xfa, 0xed, 0xfe, 0x07, 0x00, 0x00, 0x00},
	}

	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			matched := false
			for _, magic := range executableMagic {
				if bytes.HasPrefix(header, magic) {
					matched = true
				}
			}
			require.True(t, matched, "%s is a format a Go build can emit and this "+
				"guard does not recognise its signature", name)
		})
	}

	// And it must not fire on the source and fixture shapes this repository is
	// full of, or the guard is an obstruction rather than a gate.
	for _, name := range []string{
		"main.go", "README.md", "go.mod", "go.sum", "proto/buf.yaml",
		"internal/corepindrift/testdata/proxy/list",
	} {
		require.True(t, looksLikeSource(name) || isFixture(name),
			"%s must be recognised as source or fixture", name)
	}
}
