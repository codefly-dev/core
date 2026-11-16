package ciguard

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Core is codefly's structure library: it knows no consumer of it. A
// consumer's product, module or environment name in core — in code, a test, a
// fixture or a doc — is that consumer's knowledge compiled into everyone's
// library, and the next consumer inherits it. Fixtures use neutral names
// (acme, notes, assistant, billing).
//
// The names are the ones core once carried. Third-party trees core vendors or
// keeps as parser fixtures are not core's words and are skipped.
var consumerNames = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(` + strings.Join([]string{
	"obin", "obin-ai", "obinh", "obinctl", "robin", "wiki", "lastlogin", "workos", "authkit",
	"platform-obin", "platform-core", "saas-starter", "core-solutions", "infra-base", "lodestar",
}, "|") + `)([^A-Za-z0-9]|$)`)

// consumerNameList is consumerNames' alternatives, for a cheap pre-check.
var consumerNameList = []string{
	"obin", "robin", "wiki", "lastlogin", "workos", "authkit",
	"platform-core", "saas-starter", "core-solutions", "infra-base", "lodestar",
}

// mentionsAnyName reports whether lowered contains any name as a substring,
// so the line scan runs only on files that might match.
func mentionsAnyName(lowered []byte) bool {
	for _, name := range consumerNameList {
		if bytes.Contains(lowered, []byte(name)) {
			return true
		}
	}
	return false
}

// consumerNameExceptions are paths this guard does not read: third-party
// content, and this file, which has to spell the names it refuses.
var consumerNameExceptions = []string{
	".git/",
	"vendor/",
	"code/testdata/repos/",
	"internal/ciguard/consumer_names_test.go",
	// STOPGAP until the K1 move merges (branch obin-cleanup-k1 deletes these
	// paths from core); drop the three entries in that merge.
	"solutionhost/",
	"workloadidentity/",
	"docs/module-manifest.md",
}

func TestCoreNamesNoConsumer(t *testing.T) {
	root := repoRoot(t)
	var hits []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			relative += "/"
		}
		for _, exception := range consumerNameExceptions {
			if strings.HasPrefix(relative, exception) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		if !mentionsAnyName(bytes.ToLower(data)) {
			return nil
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64*1024), len(data)+1)
		for line := 1; scanner.Scan(); line++ {
			if match := consumerNames.FindStringSubmatch(scanner.Text()); match != nil {
				hits = append(hits, relative+":"+strconv.Itoa(line)+": "+match[2])
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("core names a consumer in %d place(s); use a neutral name (acme, notes, assistant, billing):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}
