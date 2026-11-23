package sourcestats

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// The tree under testdata/tree is counted by hand in these comments, file by
// file, so a change to what a line of code is shows up as a number that no
// longer matches its reason.
//
//	api/serve.go              go          5 handwritten  (a comment line and a two-line block are not code; "**/*.go" in a string opens no comment)
//	api/serve_test.go         go          7 tests
//	api/api.pb.go             go          3 generated    (by name and by head)
//	gen/made.go               go          2 generated    (by head)
//	gen/hand.go               go          2 handwritten  ("do not edit" in prose is no marker)
//	gen/listed.go             go          3 handwritten, or generated when the caller says so
//	build/build.go            go          2 handwritten  (a package named build is not build output)
//	tool.py                   python      4 handwritten  (a docstring reads as code)
//	tests/test_tool.py        python      3 tests
//	web/src/Page.tsx          typescript  3 handwritten
//	web/__tests__/page.test.tsx typescript 2 tests
//	node_modules, vendor      —           skipped
//	docs/README.md, config.yaml —         not code
func tree() Stats {
	return Stats{Schema: SchemaV1, Languages: []Language{
		{Language: "go", Handwritten: 12, Generated: 5, Tests: 7},
		{Language: "python", Handwritten: 4, Tests: 3},
		{Language: "typescript", Handwritten: 3, Tests: 2},
	}}
}

func TestCountsATreeTheWayItIsCountedByHand(t *testing.T) {
	stats, err := Count(os.DirFS("testdata/tree"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := tree(); !reflect.DeepEqual(*stats, want) {
		t.Fatalf("got  %+v\nwant %+v", *stats, want)
	}
	if err := stats.Validate(); err != nil {
		t.Fatalf("what Count writes, Validate reads: %v", err)
	}
	if stats.Handwritten() != 19 {
		t.Fatalf("handwritten over every language: %d", stats.Handwritten())
	}
}

// What the tree cannot say, the caller does: a file a generator's own
// configuration lists moves from handwritten to generated, and a path the
// repository ignores is not counted at all.
func TestTheCallerSaysWhatTheTreeCannot(t *testing.T) {
	stats, err := Count(os.DirFS("testdata/tree"), Options{
		Generated: func(name string) bool { return name == "gen/listed.go" },
		Skip: func(name string, dir bool) bool {
			return (dir && name == "build") || name == "tool.py"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{Schema: SchemaV1, Languages: []Language{
		{Language: "go", Handwritten: 7, Generated: 8, Tests: 7},
		{Language: "python", Tests: 3},
		{Language: "typescript", Handwritten: 3, Tests: 2},
	}}
	if !reflect.DeepEqual(*stats, want) {
		t.Fatalf("got  %+v\nwant %+v", *stats, want)
	}
}

func TestTheSameTreeCountsTheSame(t *testing.T) {
	first, err := Count(os.DirFS("testdata/tree"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := Count(os.DirFS("testdata/tree"), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("two counts of one tree differ:\n%+v\n%+v", first, again)
		}
	}
}

func TestAnEmptyTreeCountsNothing(t *testing.T) {
	stats, err := Count(fstest.MapFS{"README.md": {Data: []byte("# prose\n")}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Languages) != 0 || stats.Handwritten() != 0 || stats.Validate() != nil {
		t.Fatalf("got %+v", stats)
	}
}

// One file per case of what a line of code is, each counted alone.
func TestWhatALineOfCodeIs(t *testing.T) {
	for name, c := range map[string]struct {
		file, source string
		lines        int
	}{
		"blank and comment lines are not code": {"a.go", "package a\n\n// c\n\nvar A = 1\n", 2},
		"no trailing newline":                  {"a.go", "package a\nvar A = 1", 2},
		"a block comment over lines":           {"a.go", "/*\nvar Hidden = 1\n*/\npackage a\n", 1},
		"code after a block closes":            {"a.go", "/* c */ package a\n", 1},
		"two blocks then code":                 {"a.go", "/* a */ /* b */ var A = 1\n", 1},
		"a block that never closes":            {"a.go", "package a\n/* open\nvar Hidden = 1\n", 1},
		"a trailing comment is still code":     {"a.go", "var A = 1 // why\n", 1},
		"a glob in a string opens no comment":  {"a.go", "var G = \"**/*\"\nvar H = 1\n", 2},
		"python hash comments":                 {"a.py", "# c\nx = 1  # why\n", 1},
		"sql dashes":                           {"a.sql", "-- c\nSELECT 1;\n/* b */\n", 1},
		"html comments":                        {"a.html", "<!-- c -->\n<p>x</p>\n<!--\n<p>hidden</p>\n-->\n", 1},
		"ruby begin and end":                   {"a.rb", "=begin\nhidden\n=end\nx = 1\n", 1},
		"an upper-case extension":              {"A.GO", "package a\n", 1},
		"comments only":                        {"a.go", "// c\n\n/* b */\n", 0},
		"not text":                             {"a.go", "GO\x00BINARY\nvar x = 1\n", 0},
		"windows line endings":                 {"a.go", "package a\r\n\r\n// c\r\nvar A = 1\r\n", 2},
	} {
		stats, err := Count(fstest.MapFS{c.file: {Data: []byte(c.source)}}, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := stats.Handwritten(); got != c.lines {
			t.Errorf("%s: %d lines of code, want %d", name, got, c.lines)
		}
	}
}

func TestWhichFilesAToolWrote(t *testing.T) {
	for name, want := range map[string]bool{
		"api/api.pb.go":              true,
		"api/api.pb.gw.go":           true,
		"consumed_gen.go":            true,
		"client/api_pb2.py":          true,
		"client/api_pb2_grpc.py":     true,
		"web/src/gen/api_pb.ts":      true,
		"generated/go/x/y.go":        true,
		"web/app.min.js":             true,
		"api/serve.go":               false,
		"generator/main.go":          false,
		"regenerated.go":             false,
		"pkg/generatedness/check.go": false,
	} {
		if got := generatedName(name); got != want {
			t.Errorf("%s: generated by name %v, want %v", name, got, want)
		}
	}
	for head, want := range map[string]bool{
		"// Code generated by protoc-gen-go. DO NOT EDIT.\npackage a\n":                              true,
		"\n\n# @generated by a tool\nx = 1\n":                                                        true,
		"/* eslint-disable */\n// This file was automatically generated.\n":                          true,
		"package a\n\n// Written by hand; do not edit lightly.\n":                                    false,
		"package a\n\n\n\nvar a = 1\nvar b = 2\nvar c = 3\nvar d = 4\n// code generated by nobody\n": false,
		"": false,
	} {
		if got := generatedHead([]byte(head)); got != want {
			t.Errorf("%q: generated by head %v, want %v", head, got, want)
		}
	}
}

func TestWhichFilesAreTests(t *testing.T) {
	for name, want := range map[string]bool{
		"api/serve_test.go":           true,
		"tests/unit/helpers.py":       true,
		"pkg/test_client.py":          true,
		"pkg/conftest.py":             true,
		"web/src/Page.test.tsx":       true,
		"web/src/Page.spec.ts":        true,
		"web/__tests__/setup.ts":      true,
		"src/test/java/FooTest.java":  true,
		"src/main/java/FooTest.java":  true,
		"pkg/testdata/fixture.go":     true,
		"api/serve.go":                false,
		"pkg/latest.go":               false,
		"pkg/contest.py":              false,
		"pkg/testing_utils/helper.go": false,
		"pkg/attestation/verify.go":   false,
	} {
		if got := testFile(name); got != want {
			t.Errorf("%s: a test %v, want %v", name, got, want)
		}
	}
}

func TestValidateHoldsAFigureThatArrivedInARelease(t *testing.T) {
	valid := tree()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		break_ func(*Stats)
		says   string
	}{
		"another schema":      {func(s *Stats) { s.Schema = "codefly/source-stats/v2" }, "this reader reads"},
		"a name that is none": {func(s *Stats) { s.Languages[0].Language = "Go Lang" }, "is not a language name"},
		"a language twice":    {func(s *Stats) { s.Languages[1].Language = "go" }, "once each, sorted by name"},
		"out of order":        {func(s *Stats) { s.Languages[0], s.Languages[1] = s.Languages[1], s.Languages[0] }, "once each, sorted by name"},
		"a negative count":    {func(s *Stats) { s.Languages[0].Tests = -1 }, "negative count"},
		"nothing counted":     {func(s *Stats) { s.Languages[2] = Language{Language: "typescript"} }, "counts no line"},
	} {
		broken := tree()
		c.break_(&broken)
		if err := broken.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	var none *Stats
	if err := none.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("nil statistics are invalid, got %v", err)
	}
	if none.Handwritten() != 0 {
		t.Error("nil statistics count nothing")
	}
}

// A generated file is recognised by its head, and stays recognised however
// long it is: the head is read once, before the lines are counted, and is
// not looked at again after the reader has moved past it.
func TestAGeneratedFileLongerThanTheHeadIsStillGenerated(t *testing.T) {
	var source strings.Builder
	source.WriteString("// Code generated by a tool. DO NOT EDIT.\n\npackage big\n\n")
	lines := 2
	for source.Len() < 4*headBytes {
		source.WriteString("var Value" + strings.Repeat("x", 40) + " = 1\n")
		lines++
	}
	// The name says nothing: only the head marks this file.
	stats, err := Count(fstest.MapFS{"big/values.go": {Data: []byte(source.String())}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Language{{Language: "go", Generated: lines - 1}}
	if !reflect.DeepEqual(stats.Languages, want) {
		t.Fatalf("got %+v, want %+v", stats.Languages, want)
	}
}
