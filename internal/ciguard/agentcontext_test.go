package ciguard

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// An agent reads AGENTS.md on every request, so an oversized or malformed set of
// context files is worse than none: it spends context and measurably reduces
// adherence (obin-ai/handbook#68). Two parts of that standard a machine can hold
// are the root file's length budget and each skill's frontmatter contract, so
// they are checked here rather than remembered.
//
// This lives in ciguard, not in a shell script wired into a workflow, for the
// same reason the rest of this package does: `go test ./...` already runs in CI,
// so the guard needs no workflow step to be enforced.

const (
	agentContextFile   = "AGENTS.md"
	claudePointerFile  = "CLAUDE.md"
	maxRootLines       = 200
	maxSkillBodyLines  = 500
	maxNameChars       = 64
	maxDescriptionSize = 1024
)

var skillNameShape = regexp.MustCompile(`^[a-z0-9-]+$`)

func lineCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return bytes.Count(raw, []byte("\n"))
}

func TestAgentContextRootStaysWithinItsBudget(t *testing.T) {
	lines := lineCount(t, filepath.Join(repoRoot(t), agentContextFile))

	require.LessOrEqualf(t, lines, maxRootLines,
		"%s is %d lines, over the %d-line cap. Move procedure into .claude/skills/, "+
			"or context into a nested AGENTS.md beside what it describes. Do not append.",
		agentContextFile, lines, maxRootLines)
}

func TestBaseRunnerDoesNotOwnPostgresRecovery(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), "runners", "base", "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				require.NotContains(t, strings.ToLower(identifier.Name), "postgres",
					"%s: agent-specific recovery belongs in its agent", path)
			}
			return true
		})
	}
}

// One canonical source: CLAUDE.md points at AGENTS.md instead of drifting from
// it. Two files saying nearly the same thing means an agent reads whichever one
// rotted.
func TestClaudeFileIsOnlyAPointer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), claudePointerFile))
	require.NoError(t, err)

	require.Equal(t, "@"+agentContextFile, strings.TrimSpace(string(raw)),
		"%s must stay a pointer line '@%s' so there is one canonical source.",
		claudePointerFile, agentContextFile)
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// splitFrontmatter returns the YAML block a SKILL.md opens with, and the body
// that follows it.
func splitFrontmatter(t *testing.T, path string) (skillFrontmatter, []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := strings.Split(string(raw), "\n")
	require.NotEmpty(t, lines)
	require.Equalf(t, "---", lines[0],
		"%s does not open with a '---' frontmatter block, so nothing about the "+
			"skill is declared and it is never loaded.", path)

	end := -1
	for i, line := range lines[1:] {
		if line == "---" {
			end = i + 1
			break
		}
	}
	require.Positivef(t, end, "%s opens a frontmatter block that is never closed.", path)

	var front skillFrontmatter
	require.NoErrorf(t, yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &front),
		"%s has frontmatter that is not valid YAML.", path)

	return front, lines[end+1:]
}

// A skill's frontmatter is all an agent sees before deciding to load it: a name
// that does not match its directory, or a missing description, makes the skill
// unreachable without failing anything else — it rots invisibly.
func TestSkillsDeclareAUsableNameAndDescription(t *testing.T) {
	skills, err := filepath.Glob(filepath.Join(repoRoot(t), ".claude", "skills", "*", "SKILL.md"))
	require.NoError(t, err)
	require.NotEmpty(t, skills, "no skills found — the glob or the layout moved")

	for _, skill := range skills {
		directory := filepath.Base(filepath.Dir(skill))

		t.Run(directory, func(t *testing.T) {
			front, body := splitFrontmatter(t, skill)

			require.Equalf(t, directory, front.Name,
				"%s declares name %q but sits in %q; an agent resolves a skill by its "+
					"directory, so the two must agree.", skill, front.Name, directory)
			require.Regexpf(t, skillNameShape, front.Name,
				"%s name %q must be lowercase letters, digits and hyphens.", skill, front.Name)
			require.LessOrEqualf(t, len(front.Name), maxNameChars,
				"%s name is %d characters, over %d.", skill, len(front.Name), maxNameChars)

			require.NotEmptyf(t, front.Description,
				"%s has no description — an agent cannot tell when to load it.", skill)
			require.LessOrEqualf(t, len(front.Description), maxDescriptionSize,
				"%s description is %d characters, over %d.",
				skill, len(front.Description), maxDescriptionSize)

			require.LessOrEqualf(t, len(body), maxSkillBodyLines,
				"%s body is %d lines, over the %d-line cap. Split the detail into "+
					"reference files the skill reads when it needs them.",
				skill, len(body), maxSkillBodyLines)
		})
	}
}
