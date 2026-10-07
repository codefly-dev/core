package solutionhost_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// classifyByRoot is the shape of classification a producer supplies: the
// service a path belongs to decides its side. Here everything under web/ is
// the frontend and everything else the backend.
func classifyByRoot(path string) solutionhost.Surface {
	if strings.HasPrefix(path, "web/") {
		return solutionhost.SurfaceFrontend
	}
	return solutionhost.SurfaceBackend
}

// writeTree materializes files under a fresh directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return root
}

func sourceTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"module.codefly.yaml":            "kind: module\nname: alpha\nvendored:\n  - web/src/clients\n",
		"services/api/main.go":           "package main\n\nfunc main() {}\n",
		"services/api/util.go":           "x\r\ny\r\n\r\n",
		"services/api/blank.go":          "\n\n  \t \n",
		"services/api/api.proto":         "syntax = \"proto3\";\n",
		"tools/blank.py":                 "   \n\t\n",
		"web/src/index.tsx":              "a\nb\nc",
		"web/src/legacy.js":              "// legacy\n",
		"web/src/clients/go/client.go":   "package client\nvar vendored = true\n",
		"web/src/clients/api.ts":         "export const vendored = 1\n",
		"web/src/clients-of-ours/own.ts": "export const own = 1\n",
		"README.md":                      "words\nwords\n",
		"Dockerfile":                     "FROM scratch\n",
	})
}

// The counter's definition of a line, applied over a tree: non-blank lines of
// files whose extension names a language, outside the vendored prefixes,
// attributed to the side the producer names, one row per language with a
// counted line, totals summed, the exclusion list as declared.
func TestMeasureDirCountsNonBlankLinesPerLanguageAndSide(t *testing.T) {
	root := sourceTree(t)
	size, err := solutionhost.MeasureDir(root, []string{"web/src/clients"}, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, &solutionhost.BuildSize{
		Languages: &[]solutionhost.LanguageSize{
			{Language: solutionhost.LanguageGo, Backend: 4},
			{Language: solutionhost.LanguageJavaScript, Frontend: 1},
			{Language: solutionhost.LanguageTypeScript, Frontend: 4},
		},
		Backend:  4,
		Frontend: 5,
		Total:    9,
		Vendored: &[]string{"web/src/clients"},
	}, size, "go: 2+2 lines, blank-only file adds none; python: blank-only, so no row; "+
		"tsx: 3 lines without a trailing newline; own.ts: 1, under a sibling that merely shares the prefix's letters; "+
		"js: a comment counts; the vendored client kit is excluded; proto, markdown and the Dockerfile are no language")
	require.NoError(t, size.Validate())

	// What a producer does with it: into a document, written through Marshal,
	// read back as the same thing.
	document := parse(t, "build-size")
	document.BuildSize = size
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	again, err := solutionhost.Parse(written)
	require.NoError(t, err)
	require.Equal(t, size, again.BuildSize)

	// Without the declaration the kit is counted too, and the document says
	// that nothing was excluded rather than saying nothing.
	all, err := solutionhost.MeasureDir(root, nil, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, uint64(12), all.Total, "the kit's two files add two go lines and one typescript line, all frontend, since they live under web/")
	require.Equal(t, uint64(8), all.Frontend)
	require.Equal(t, &[]string{}, all.Vendored)
}

// The counter is fed by the producer's own walk — the one that digests the
// release — so the two facts can describe one tree. Add answers whether a file
// was counted, skips a vendored or unknown file without reading it, and
// refuses a path fed twice: a walk that reaches a file twice describes a build
// that does not exist.
func TestACounterIsFedOneFileAtATime(t *testing.T) {
	counter, err := solutionhost.NewCounter([]string{"web/src/clients"})
	require.NoError(t, err)

	counted, err := counter.Add("services/api/main.go", solutionhost.SurfaceBackend, strings.NewReader("package main\n\nfunc main() {}\n"))
	require.NoError(t, err)
	require.True(t, counted)
	counted, err = counter.Add("web/src/clients/api.ts", solutionhost.SurfaceFrontend, failingReader{})
	require.NoError(t, err, "a vendored file is not read at all")
	require.False(t, counted)
	counted, err = counter.Add("README.md", solutionhost.SurfaceBackend, failingReader{})
	require.NoError(t, err, "a file in no language is not read at all")
	require.False(t, counted)
	require.True(t, counter.Skips("web/src/clients/deep/x.go"))
	require.True(t, counter.Skips("notes.txt"))
	require.False(t, counter.Skips("web/src/index.ts"))

	_, err = counter.Add("services/api/main.go", solutionhost.SurfaceBackend, strings.NewReader("package main\n"))
	require.ErrorContains(t, err, `cannot count "services/api/main.go" twice`)
	_, err = counter.Add("README.md", solutionhost.SurfaceBackend, failingReader{})
	require.ErrorContains(t, err, "twice", "a path is one file whether or not it was counted")

	size, err := counter.Result()
	require.NoError(t, err)
	require.Equal(t, uint64(2), size.Total, "the refused repeat added nothing")
	require.Equal(t, []string{"web/src/clients"}, *size.Vendored)
	require.NoError(t, size.Validate())

	// Counting goes on after a Result.
	counted, err = counter.Add("web/src/index.ts", solutionhost.SurfaceFrontend, strings.NewReader("a\nb\n"))
	require.NoError(t, err)
	require.True(t, counted)
	size, err = counter.Result()
	require.NoError(t, err)
	require.Equal(t, uint64(4), size.Total)
	require.Equal(t, uint64(2), size.Frontend)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, os.ErrInvalid }

// What the counter refuses: a vendored declaration the document would refuse,
// a side that is not one of the two, a path not in the one spelling — a path
// that is not UTF-8 included, since the signing encoding would rewrite it —
// and a file that cannot be read.
func TestACounterRefusesWhatItCannotCount(t *testing.T) {
	for name, vendored := range map[string][]string{
		"trailing slash": {"web/src/clients/"},
		"nested":         {"web/src/clients", "web/src/clients/go"},
		"twice":          {"sdk", "sdk"},
		"escaping":       {"../other"},
		"not utf-8":      {"vendor/\xff"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.NewCounter(vendored)
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.Contains(t, err.Error(), "build_size vendored path")
		})
	}
	counter, err := solutionhost.NewCounter(nil)
	require.NoError(t, err)

	_, err = counter.Add("sdk/client.go", solutionhost.SurfaceClient, strings.NewReader("x\n"))
	require.ErrorContains(t, err, "a line is backend or frontend")
	_, err = counter.Add("./sdk/client.go", solutionhost.SurfaceBackend, strings.NewReader("x\n"))
	require.ErrorContains(t, err, "in one spelling")
	_, err = counter.Add("/sdk/client.go", solutionhost.SurfaceBackend, strings.NewReader("x\n"))
	require.ErrorContains(t, err, "in one spelling")
	_, err = counter.Add("sdk/\xff.go", solutionhost.SurfaceBackend, strings.NewReader("x\n"))
	require.ErrorContains(t, err, "valid UTF-8")
	_, err = counter.Add("sdk/client.go", solutionhost.SurfaceBackend, nil)
	require.ErrorContains(t, err, "no content was given")
	_, err = counter.Add("sdk/other.go", solutionhost.SurfaceBackend, failingReader{})
	require.ErrorContains(t, err, `cannot count "sdk/other.go"`)
	_, err = counter.Add("sdk/clie\u006e\u0074\u0065\u0301.go", solutionhost.SurfaceBackend, strings.NewReader("x\n"))
	require.ErrorContains(t, err, "in one spelling", "a decomposed path is refused where it is read, not silently unmatched")
	size, err := counter.Result()
	require.NoError(t, err)
	require.Equal(t, uint64(0), size.Total, "nothing refused was counted")
}

// The line definition at its edges: a line is counted once however long it
// is, whitespace-only lines are not lines, a final line needs no newline, and
// a byte that is not ASCII whitespace — a byte-order mark included — makes a
// line.
func TestTheLineDefinitionAtItsEdges(t *testing.T) {
	for name, file := range map[string]struct {
		content string
		lines   uint64
	}{
		"one very long line":     {strings.Repeat("a", 300_000), 1},
		"long line then another": {strings.Repeat("a", 300_000) + "\nb\n", 2},
		"whitespace only":        {" \t\r\n\f\v\n   \n", 0},
		"no trailing newline":    {"a", 1},
		"crlf":                   {"a\r\nb\r\n", 2},
		"blank lines between":    {"a\n\n\n\nb\n", 2},
		"byte-order mark alone":  {"\xef\xbb\xbf\n", 1},
		"non-ascii space":        {"\xc2\xa0\n", 1},
		"empty":                  {"", 0},
		"only newlines":          {"\n\n\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			counter, err := solutionhost.NewCounter(nil)
			require.NoError(t, err)
			counted, err := counter.Add("main.go", solutionhost.SurfaceBackend, strings.NewReader(file.content))
			require.NoError(t, err)
			require.True(t, counted)
			size, err := counter.Result()
			require.NoError(t, err)
			require.Equal(t, file.lines, size.Backend)
		})
	}
}

// A link listed by the walk is passed over as the release digest passes it
// over, a vendored directory is never ENTERED — proven by making the directory
// itself unlistable, so a walk that entered it to skip its files one by one
// would fail — and the result is what the plain tree gave.
func TestMeasureDirSkipsLinksAndNeverEntersAVendoredDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{
		"services/api/main.go":   "package main\nfunc main() {}\n",
		"web/src/index.ts":       "a\nb\n",
		"web/src/clients/api.ts": "vendored\n",
	})
	clients := filepath.Join(root, "web", "src", "clients")
	require.NoError(t, os.Chmod(clients, 0o000))
	t.Cleanup(func() { _ = os.Chmod(clients, 0o755) })
	// A relative link, as a link inside a package is written; an absolute one
	// os.Root would refuse on its own, which is not what this witnesses.
	require.NoError(t, os.Symlink("main.go", filepath.Join(root, "services", "api", "linked.go")))

	size, err := solutionhost.MeasureDir(root, []string{"web/src/clients"}, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, uint64(2), size.Backend, "the link is not a regular file and is not counted twice")
	require.Equal(t, uint64(2), size.Frontend)
	require.Equal(t, uint64(4), size.Total)

	// Without the exclusion the directory is entered, cannot be listed, and
	// the failure names it rather than passing silently.
	_, err = solutionhost.MeasureDir(root, nil, classifyByRoot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/src/clients")
}

// The tree is read through a directory descriptor, so a directory swapped for
// a symbolic link to somewhere OUTSIDE the package while the walk runs — after
// the walk listed the directory, before a file under it is opened — cannot
// lead the count outside: os.Root refuses the escape and the measurement fails
// naming the path. The swap happens inside the classification callback, which
// the walk asks right before it opens the file.
func TestMeasureDirRefusesADirectorySwappedForALinkOutsideTheTree(t *testing.T) {
	root := writeTree(t, map[string]string{
		"services/api/main.go": "package main\n",
		"web/src/index.ts":     "inside\n",
	})
	outside := writeTree(t, map[string]string{
		"src/index.ts": strings.Repeat("outside\n", 1000),
	})
	swapped := false
	classify := func(path string) solutionhost.Surface {
		if path == "web/src/index.ts" && !swapped {
			swapped = true
			require.NoError(t, os.RemoveAll(filepath.Join(root, "web", "src")))
			require.NoError(t, os.Symlink(filepath.Join(outside, "src"), filepath.Join(root, "web", "src")))
		}
		return classifyByRoot(path)
	}
	size, err := solutionhost.MeasureDir(root, nil, classify)
	require.True(t, swapped, "the swap must have happened mid-walk")
	require.Error(t, err, "a link leading outside the tree is refused, never followed")
	require.Contains(t, err.Error(), "web/src/index.ts")
	require.Nil(t, size)
}

// And a FILE swapped for a link, to a file inside or outside the tree, is
// refused rather than followed: the entry is looked at again through the root
// right before it is opened. The inside case uses a RELATIVE link, as a link
// inside a package is written — an absolute one os.Root refuses on its own,
// which would leave the re-check unwitnessed; with the re-check deleted, this
// case counts the target's lines under the swapped path.
func TestMeasureDirRefusesAFileSwappedForALink(t *testing.T) {
	for name, target := range map[string]func(root, outside string) string{
		"inside the tree":  func(_, _ string) string { return "../../services/api/main.go" },
		"outside the tree": func(_, outside string) string { return filepath.Join(outside, "big.go") },
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"services/api/main.go": "package main\n",
				"web/src/index.ts":     "inside\n",
			})
			outside := writeTree(t, map[string]string{"big.go": strings.Repeat("x\n", 1000)})
			swapped := false
			classify := func(path string) solutionhost.Surface {
				if path == "web/src/index.ts" && !swapped {
					swapped = true
					require.NoError(t, os.Remove(filepath.Join(root, "web", "src", "index.ts")))
					require.NoError(t, os.Symlink(target(root, outside), filepath.Join(root, "web", "src", "index.ts")))
				}
				return classifyByRoot(path)
			}
			_, err := solutionhost.MeasureDir(root, nil, classify)
			require.True(t, swapped)
			require.Error(t, err)
			require.Contains(t, err.Error(), "web/src/index.ts")
		})
	}
}

// A DIRECTORY swapped for a relative link inside the tree after the walk
// listed it and before the walk descends into it is refused rather than
// followed: os.Root would follow a link that stays inside the tree, and the
// vendored directory's files would then be counted under the swapped path, or
// an already-counted directory counted twice. The directory is looked at
// again through the root before the walk descends. The swap has to hit a
// directory the walk has ALREADY listed — a top-level one, recorded by the
// root listing at the start — and happens while an earlier top-level file is
// being classified; a nested directory swapped before its parent is listed is
// simply listed as a link and passed over, which witnesses nothing.
func TestMeasureDirRefusesADirectorySwappedForALinkInsideTheTree(t *testing.T) {
	for name, target := range map[string]string{
		"to the vendored directory":      "vendor",
		"to a directory already counted": "services",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"a.go":                 "package a\n",
				"services/api/main.go": "package main\n",
				"vendor/kit/big.go":    strings.Repeat("x\n", 50),
				"zeta/last.go":         "package zeta\n",
			})
			swapped := false
			classify := func(path string) solutionhost.Surface {
				// a.go is classified before the walk reaches zeta/, which the
				// root listing has already recorded as a directory.
				if path == "a.go" && !swapped {
					swapped = true
					require.NoError(t, os.RemoveAll(filepath.Join(root, "zeta")))
					require.NoError(t, os.Symlink(target, filepath.Join(root, "zeta")))
				}
				return classifyByRoot(path)
			}
			_, err := solutionhost.MeasureDir(root, []string{"vendor"}, classify)
			require.True(t, swapped, "the swap must have happened mid-walk")
			require.Error(t, err, "a directory that became a link is refused, not descended into")
			require.Contains(t, err.Error(), "zeta")
		})
	}
}

// A measurement needs a directory it can open and a classification; a
// classification that answers a third side fails the measurement at the file
// that got it.
func TestMeasureDirRequiresATreeAndAClassification(t *testing.T) {
	root := sourceTree(t)
	_, err := solutionhost.MeasureDir(filepath.Join(root, "no-such-dir"), nil, classifyByRoot)
	require.Error(t, err)
	_, err = solutionhost.MeasureDir(root, nil, nil)
	require.ErrorContains(t, err, "no classification")
	_, err = solutionhost.MeasureDir(root, nil, func(string) solutionhost.Surface { return solutionhost.SurfaceClient })
	require.ErrorContains(t, err, "a line is backend or frontend")
	_, err = solutionhost.MeasureDir(root, []string{"web/src/clients/"}, classifyByRoot)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}

// A declared exclusion is signed as "excluded", so one that excluded nothing
// is refused by name rather than signed: a typo, a directory stored under
// another Unicode spelling, a link where a directory was expected. Coverage is
// earned through Skips or Add — files in no language included — or through
// the directory the walk declines to enter.
func TestADeclaredExclusionThatCoversNothingIsRefused(t *testing.T) {
	root := writeTree(t, map[string]string{
		"services/api/main.go":   "package main\n",
		"web/src/index.ts":       "a\n",
		"web/src/clients/api.ts": "vendored\n",
		"web/src/clients/README": "words\n",
		"web/kit.ts":             "generated\n",
	})
	// A typo covers nothing.
	_, err := solutionhost.MeasureDir(root, []string{"web/src/clinets"}, classifyByRoot)
	require.Error(t, err)
	require.Contains(t, err.Error(), `vendored path "web/src/clinets" excluded nothing`)

	// The directory spelled right covers, through the walk declining to enter it.
	size, err := solutionhost.MeasureDir(root, []string{"web/src/clients"}, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, uint64(3), size.Total)

	// A prefix naming one FILE covers through that file, whatever its language.
	size, err = solutionhost.MeasureDir(root, []string{"web/kit.ts", "web/src/clients/README"}, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, []string{"web/kit.ts", "web/src/clients/README"}, *size.Vendored)
	require.Equal(t, uint64(3), size.Total, "kit.ts is excluded; README was never code")

	// A prefix naming a LINK covers nothing: the walk passes the link over,
	// and the target is counted under its own path. The declaration meant
	// the target, and did not exclude it.
	require.NoError(t, os.Symlink(filepath.Join(root, "web", "src", "clients"), filepath.Join(root, "web", "linked")))
	_, err = solutionhost.MeasureDir(root, []string{"web/linked"}, classifyByRoot)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"web/linked" excluded nothing`)

	// Through the counter directly: Skips and Add both earn coverage, and a
	// prefix nothing was offered under is refused at Result.
	counter, err := solutionhost.NewCounter([]string{"vendor/a", "vendor/b"})
	require.NoError(t, err)
	require.True(t, counter.Skips("vendor/a/notes.txt"), "a file in no language, offered, still covers")
	_, err = counter.Result()
	require.ErrorContains(t, err, `"vendor/b" excluded nothing`)
	counted, err := counter.Add("vendor/b/x.go", solutionhost.SurfaceBackend, failingReader{})
	require.NoError(t, err)
	require.False(t, counted)
	_, err = counter.Result()
	require.NoError(t, err)
}

// A directory stored under the decomposed Unicode spelling against a manifest
// that declares the composed one: a byte comparison never matches, so the
// exclusion would silently cover nothing and be signed as applied. The grammar
// holds the declaration to NFC, the walk refuses a decomposed path where it is
// read, and a composed declaration over a decomposed tree is refused as
// covering nothing rather than signed.
func TestAnExclusionInAnotherUnicodeSpellingIsNotSignedAsApplied(t *testing.T) {
	composed, decomposed := "web/client\u00e9", "web/clie\u006e\u0074\u0065\u0301"
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(decomposed)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(decomposed), "a.ts"), []byte("a\nb\nc\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600))

	entries, err := os.ReadDir(filepath.Join(root, "web"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	if entries[0].Name() == "client\u00e9" {
		t.Skip("this filesystem normalizes names to NFC, so the two spellings are one here")
	}

	// The decomposed declaration is not in the grammar.
	_, err = solutionhost.NewCounter([]string{decomposed})
	require.ErrorIs(t, err, solutionhost.ErrInvalid)

	// The composed declaration over the decomposed tree: the walk reaches a
	// path in the other spelling and refuses it where it is read, so the
	// measurement fails rather than signing the exclusion as applied.
	_, err = solutionhost.MeasureDir(root, []string{composed}, classifyByRoot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "in one spelling")
}

// The pre-open type check's own witness: a file swapped for a FIFO while an
// earlier file is classified. Lstat sees the FIFO and refuses before anything
// is opened — an open would block. With the pre-open check deleted this test
// does not fail, it hangs, which is the stronger statement of what the check
// is for.
func TestMeasureDirRefusesAFileThatBecameAFIFOBeforeOpening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs here")
	}
	// Both files in ONE directory, listed together before either is visited:
	// a swap must hit an entry the walk has already recorded as a regular
	// file, or the fresh listing sees a FIFO and passes it over, which
	// witnesses nothing.
	root := writeTree(t, map[string]string{
		"services/a.go":    "package a\n",
		"services/main.go": "package main\n",
	})
	swapped := false
	classify := func(path string) solutionhost.Surface {
		if path == "services/a.go" && !swapped {
			swapped = true
			target := filepath.Join(root, "services", "main.go")
			require.NoError(t, os.Remove(target))
			require.NoError(t, syscall.Mkfifo(target, 0o600))
		}
		return classifyByRoot(path)
	}
	_, err := solutionhost.MeasureDir(root, nil, classify)
	require.True(t, swapped)
	require.Error(t, err)
	require.Contains(t, err.Error(), "services/main.go")
	require.Contains(t, err.Error(), "is no longer a regular file")
}

// A directory entry offered under a prefix earns its coverage, so MeasureDir —
// which records the directory it declines to enter — and a producer feeding
// Skips from a walk that visits directories agree on an EMPTY vendored
// directory: it exists, excluding it is not a typo, and both accept.
func TestADirectoryEntryEarnsCoverageForAnEmptyVendoredDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{"services/main.go": "package main\n"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "vendor", "kit"), 0o755))
	size, err := solutionhost.MeasureDir(root, []string{"vendor"}, classifyByRoot)
	require.NoError(t, err)
	require.Equal(t, []string{"vendor"}, *size.Vendored)

	counter, err := solutionhost.NewCounter([]string{"vendor"})
	require.NoError(t, err)
	_, err = counter.Result()
	require.ErrorContains(t, err, `"vendor" excluded nothing`, "nothing offered yet")
	require.True(t, counter.Skips("vendor"), "the directory entry itself, as a walk visits it")
	_, err = counter.Result()
	require.NoError(t, err, "the directory entry earned the coverage")
}
