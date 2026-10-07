package solutionhost

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/codefly-dev/core/resources/names"
)

// Counter accumulates the build's size one file at a time.
//
// It exists so the producer can count the files it digests, as it digests
// them. The renderer already walks the module tree once to compute the release
// digest, and the honest definition of "the build's size" is that same walk
// minus the vendored prefixes: feeding each file through Add as it is hashed
// lets the two facts describe one tree, with no second walk to disagree with
// the first. The counter cannot see the digest, so it does not PROVE the
// producer fed it the digested bytes — that proof is the producer's own test
// over its renderer — but it holds what it can: a path is counted once, in one
// spelling, under one of two sides. MeasureDir wraps a Counter around a walk of
// a directory opened as an os.Root, for a producer that holds a tree and no
// walk of its own; both go through the one Add.
//
// What a counted line is, defined once: a line of a file whose extension names
// a Language (LanguageOf), holding at least one byte that is not ASCII
// whitespace. Comments count; blank lines do not. Stripping comments would need
// a grammar per language, and the one place core has those is its cgo surface,
// which a presence document's readers must not link. The number is therefore
// one a reader can reproduce with nothing but this package, and it moves only
// when the code does.
type Counter struct {
	vendored []string
	rows     map[Language]*LanguageSize
	// seen is every path Add was given, counted or not: a path fed twice is
	// a producer walking something twice, and a count that doubled a file
	// would describe a build that does not exist.
	seen map[string]struct{}
	// covered is every declared prefix that has covered at least one path
	// offered to the counter, through Skips or Add. A declared exclusion is
	// signed as "excluded", so one that covered nothing — a typo, a renamed
	// directory, a directory spelled in another Unicode form, a link where a
	// directory was expected — is refused by Result rather than signed.
	covered map[string]struct{}
}

// NewCounter starts a count that excludes the vendored prefixes, which are
// held to the rules the document holds them to — canonical spelling, each once,
// none under another — so a declaration the counter accepts is one the section
// it produces is accepted with. The list is the manifest's
// (resources.Module.Vendored), passed through unchanged.
func NewCounter(vendored []string) (*Counter, error) {
	declared := slices.Clone(vendored)
	if declared == nil {
		declared = []string{}
	}
	probe := &BuildSize{Languages: &[]LanguageSize{}, Vendored: &declared}
	if err := probe.Validate(); err != nil {
		return nil, fmt.Errorf("build size cannot exclude the declared vendored paths: %w", err)
	}
	return &Counter{vendored: declared, rows: map[Language]*LanguageSize{}, seen: map[string]struct{}{}, covered: map[string]struct{}{}}, nil
}

// Skips reports whether an entry at path contributes nothing to the count: it
// lies under a vendored prefix, or its extension names no language. A producer
// walking its own tree asks this for EVERY entry its walk visits before
// opening a file — files in no language and directories included — and
// MeasureDir does not enter a directory it answers true for. Asking is what
// records that a declared prefix covered something: a prefix nothing was ever
// offered under is one Result refuses. A directory entry counts as something
// (an empty vendored directory exists, and excluding it is not a typo), which
// is why a producer offers directories too: MeasureDir records the directory
// it declines to enter, and a producer that offered only files would refuse a
// tree MeasureDir accepts.
func (counter *Counter) Skips(path string) bool {
	if counter.excludes(path) {
		return true
	}
	_, known := LanguageOf(path)
	return !known
}

// excludes reports whether path lies under a declared prefix, and records the
// prefix as having covered a path when it does.
func (counter *Counter) excludes(path string) bool {
	for _, prefix := range counter.vendored {
		if names.PathWithin(prefix, path) {
			counter.covered[prefix] = struct{}{}
			return true
		}
	}
	return false
}

// Add counts one file. path is its slash-separated path relative to the tree's
// root, in names.IsPathPrefix's spelling — valid UTF-8 in normalization form
// C — so a vendored prefix matches a file spelled the way the manifest spells
// it, and a walked path in another form is refused here rather than silently
// unmatched; surface is the side its lines are attributed to, backend or
// frontend, which the producer knows from the service the file belongs to. A
// path given twice is refused, counted or not. A file Skips reports is not
// counted and content is not read, but it is offered — which is how a declared
// prefix earns its place in the result — so a producer feeds EVERY path it
// digests, files in no language included; counted reports whether this one
// was.
func (counter *Counter) Add(path string, surface Surface, content io.Reader) (counted bool, err error) {
	if !names.IsPathPrefix(path) {
		return false, fmt.Errorf("build size cannot count %q: a file path is slash-separated, relative to the tree, valid UTF-8 and in one spelling", path)
	}
	if !slices.Contains(sizeSurfaces, surface) {
		return false, fmt.Errorf("build size cannot count %q as %q: a line is %s or %s", path, surface, SurfaceBackend, SurfaceFrontend)
	}
	if _, twice := counter.seen[path]; twice {
		return false, fmt.Errorf("build size cannot count %q twice: a path is one file, and a walk that reaches it twice describes a build that does not exist", path)
	}
	counter.seen[path] = struct{}{}
	if counter.Skips(path) {
		return false, nil
	}
	if content == nil {
		return false, fmt.Errorf("build size cannot count %q: no content was given", path)
	}
	lines, err := countLines(content)
	if err != nil {
		return false, fmt.Errorf("build size cannot count %q: %w", path, err)
	}
	language, _ := LanguageOf(path)
	row := counter.rows[language]
	if row == nil {
		row = &LanguageSize{Language: language}
		counter.rows[language] = row
	}
	if surface == SurfaceBackend {
		row.Backend += lines
	} else {
		row.Frontend += lines
	}
	return true, nil
}

// Result returns the build's size as counted so far: one row per language with
// a counted line, sorted, the totals summed, and the vendored list as declared
// — PROVIDED every declared prefix covered at least one path offered to the
// counter. A prefix that covered nothing is refused by name: the document
// signs the list as "excluded", and an exclusion that excluded nothing — a
// typo, a directory renamed or spelled in another Unicode form, a link where a
// directory was expected — would be signed as applied while its lines sat in
// the totals, and no reader could tell. The result is a section Validate
// accepts, and the Counter may go on counting.
func (counter *Counter) Result() (*BuildSize, error) {
	var uncovered []string
	for _, prefix := range counter.vendored {
		if _, ok := counter.covered[prefix]; !ok {
			uncovered = append(uncovered, fmt.Sprintf("%q", prefix))
		}
	}
	if len(uncovered) != 0 {
		return nil, fmt.Errorf("build size: vendored path %s excluded nothing: the walk offered no file or directory under it, so the declaration does not describe this tree — a typo, a directory renamed or spelled in another Unicode form, or a link where a directory was expected; a path that excludes nothing is not signed as excluded",
			strings.Join(uncovered, ", "))
	}
	rows := make([]LanguageSize, 0, len(counter.rows))
	size := &BuildSize{}
	for _, row := range counter.rows {
		if row.Backend == 0 && row.Frontend == 0 {
			continue
		}
		rows = append(rows, *row)
		size.Backend += row.Backend
		size.Frontend += row.Frontend
	}
	size.Total = size.Backend + size.Frontend
	excluded := slices.Clone(counter.vendored)
	size.Languages, size.Vendored = &rows, &excluded
	return size.canonical(), nil
}

// countLines counts the lines of content holding a byte that is not ASCII
// whitespace, in one pass over the bytes, so a minified file whose one line is
// megabytes long costs no more memory than a short one.
func countLines(content io.Reader) (uint64, error) {
	var lines uint64
	pending := false
	buffer := make([]byte, 32*1024)
	for {
		read, err := content.Read(buffer)
		for _, b := range buffer[:read] {
			switch b {
			case '\n':
				if pending {
					lines++
					pending = false
				}
			case ' ', '\t', '\r', '\f', '\v':
			default:
				pending = true
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if pending {
		lines++
	}
	return lines, nil
}

// betweenLookAndOpen is a seam for the package's own tests: called, when set,
// between a file being looked at (Lstat) and being opened, so the one swap
// that only os.SameFile can refuse — a regular file replaced by another
// regular file in that window — can be made to happen deterministically. It is
// nil everywhere but in a test.
var betweenLookAndOpen func(path string)

// MeasureDir counts a whole tree through one Counter: every regular file under
// dir, by its slash path, attributed to the surface classify answers for it,
// which is asked before the file is opened.
//
// The tree is opened as an os.Root and every file is opened THROUGH it, so a
// path is resolved relative to the root's descriptor and never as a path the
// operating system walks from scratch. That is what holds the count inside the
// package while the tree is being read: a directory swapped for a symbolic
// link to somewhere outside dir between the walk listing it and a file under
// it being opened is refused by os.Root rather than followed, and the
// measurement fails naming the path. An entry the walk lists as a link is
// passed over, as the release digest passes it over. Every entry is looked at
// again THROUGH THE ROOT right before it is used — a directory before the walk
// descends into it, a file before it is opened — and one that has become a
// link since the walk listed it is refused rather than followed; a file
// swapped for another between being looked at and being opened is refused too,
// since the opened descriptor must be the very file Lstat described
// (os.SameFile). What remains is the moment between a directory's re-check
// and its listing: a link made in that window and pointing inside dir is
// followed by os.Root, and the files under it are counted under the swapped
// path, vendored or not. That is a producer racing its own tree, it reaches
// nothing outside the package, and the renderer's Add-as-you-hash path has no
// such window because it opens nothing twice. Every declared vendored prefix
// must cover something the walk offered, or the measurement fails naming it;
// see Counter.Result.
func MeasureDir(dir string, vendored []string, classify func(path string) Surface) (*BuildSize, error) {
	if classify == nil {
		return nil, errors.New("build size: no classification; every counted file is backend or frontend, and the producer knows which")
	}
	counter, err := NewCounter(vendored)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("build size: %w", err)
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if entry.IsDir() {
			if counter.excludes(path) {
				return fs.SkipDir
			}
			// Looked at again through the root before the walk descends: the
			// listing that called this a directory is a moment old, and a
			// directory that has become a link since — to a vendored
			// directory, say, or to one already counted — would otherwise be
			// followed by os.Root and its files counted under this path.
			if info, err := root.Lstat(path); err != nil || !info.IsDir() {
				return fmt.Errorf("%s: is no longer a directory", path)
			}
			return nil
		}
		if !entry.Type().IsRegular() || counter.Skips(path) {
			return nil
		}
		surface := classify(path)
		// Looked at again, through the root, right before it is opened: the
		// walk's listing is a moment ago, and an entry that has become a link
		// since is refused rather than followed. The type is checked BEFORE
		// the open as well as after (os.SameFile below) because an entry that
		// has become a FIFO would block the open itself; each has its own
		// witness, the second through the seam below.
		info, err := root.Lstat(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: is no longer a regular file", path)
		}
		if betweenLookAndOpen != nil {
			betweenLookAndOpen(path)
		}
		file, err := root.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return fmt.Errorf("%s: what was opened is not the regular file that was looked at", path)
		}
		_, err = counter.Add(path, surface, file)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("build size: %w", err)
	}
	return counter.Result()
}
