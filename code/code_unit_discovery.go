package code

// ARCHITECTURE: Codefly owns structural source discovery because the Code
// boundary is the only layer allowed to inspect project paths. Consumers such
// as Mind receive typed code-unit evidence and never maintain ecosystem marker
// registries of their own. Discovery is language-neutral and executes no build,
// package-manager, or test command.

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

type codeUnitDeclaration struct {
	language       string
	runtimeAgent   string
	priority       int
	aggregatesTree bool
	requiresSource bool
	match          func(string) bool
}

type discoveredDeclaration struct {
	directory string
	manifest  string
	codeUnitDeclaration
}

type nodeSourceEvidence struct {
	directory string
	language  string
}

type nodeLanguageEvidence struct {
	javascript bool
	typescript bool
}

type codeUnitAccumulator struct {
	path            string
	primaryLanguage string
	primaryPriority int
	languages       map[string]struct{}
	manifests       map[string]struct{}
	runtimeAgents   map[string]struct{}
}

// codeUnitDeclarations is the single structural ecosystem registry for
// Codefly. Entries identify source boundaries and runtime families only; they
// never encode native commands. Order is the deterministic primary-language
// precedence for a genuinely co-located polyglot boundary.
var codeUnitDeclarations = []codeUnitDeclaration{
	{language: "go", runtimeAgent: "go", priority: 10, match: exactManifest("go.mod")},
	{language: "python", runtimeAgent: "python", priority: 20, match: exactManifest("pyproject.toml", "setup.py", "setup.cfg")},
	// Dependency inventories are valid roots and useful evidence beside a
	// strong Python declaration, but a nested requirements file alone is often
	// a fixture or input to the enclosing project. Require source in that
	// subtree before promoting it to an independently executable code unit.
	{language: "python", runtimeAgent: "python", priority: 20, requiresSource: true, match: exactManifest("uv.lock", "requirements.in", "requirements.txt")},
	{language: "typescript", runtimeAgent: "nextjs", priority: 30, match: exactManifest("package.json")},
	{language: "rust", runtimeAgent: "rust", priority: 40, match: exactManifest("Cargo.toml")},
	{language: "swift", runtimeAgent: "swift", priority: 50, match: exactManifest("Package.swift")},
	{language: "jvm", runtimeAgent: "generic", priority: 60, match: exactManifest("pom.xml", "build.gradle", "build.gradle.kts")},
	{language: "dotnet", runtimeAgent: "generic", priority: 70, aggregatesTree: true, match: manifestSuffix(".sln")},
	{language: "dotnet", runtimeAgent: "generic", priority: 71, match: manifestSuffix(".csproj", ".fsproj", ".vbproj")},
	{language: "ruby", runtimeAgent: "generic", priority: 80, match: exactManifest("Gemfile")},
	{language: "php", runtimeAgent: "generic", priority: 90, match: exactManifest("composer.json")},
	{language: "elixir", runtimeAgent: "generic", priority: 100, match: exactManifest("mix.exs")},
	{language: "cpp", runtimeAgent: "generic", priority: 110, match: exactManifest("CMakeLists.txt")},
	{language: "zig", runtimeAgent: "generic", priority: 120, match: exactManifest("build.zig", "build.zig.zon")},
	{language: "haskell", runtimeAgent: "generic", priority: 130, match: manifestSuffix(".cabal")},
}

func exactManifest(names ...string) func(string) bool {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = struct{}{}
	}
	return func(name string) bool {
		_, ok := wanted[strings.ToLower(name)]
		return ok
	}
}

func manifestSuffix(suffixes ...string) func(string) bool {
	return func(name string) bool {
		name = strings.ToLower(name)
		for _, suffix := range suffixes {
			if strings.HasSuffix(name, strings.ToLower(suffix)) {
				return true
			}
		}
		return false
	}
}

func (s *DefaultCodeServer) discoverCodeUnits(ctx context.Context, _ *codev0.DiscoverCodeUnitsRequest) (*codev0.CodeResponse, error) {
	declarations, err := s.scanCodeUnitDeclarations(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover code units: %w", err)
	}
	units := assembleCodeUnits(s.SourceDir, declarations)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_DiscoverCodeUnits{
		DiscoverCodeUnits: &codev0.DiscoverCodeUnitsResponse{CodeUnits: units},
	}}, nil
}

// scanCodeUnitDeclarations walks through the Code server's filesystem
// abstraction. This keeps discovery usable in every execution box without
// leaking host filesystem access into callers such as Mind.
func (s *DefaultCodeServer) scanCodeUnitDeclarations(ctx context.Context) ([]discoveredDeclaration, error) {
	var declarations []discoveredDeclaration
	var nodeSources []nodeSourceEvidence
	var pythonSourceDirectories []string
	err := s.FS.WalkDir(s.SourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(s.SourceDir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." && skipCodeUnitDiscoveryDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		for _, declaration := range codeUnitDeclarations {
			if !declaration.match(entry.Name()) {
				continue
			}
			relative = filepath.ToSlash(relative)
			directory := filepath.ToSlash(filepath.Dir(relative))
			if directory == "" {
				directory = "."
			}
			declarations = append(declarations, discoveredDeclaration{
				directory: directory, manifest: relative, codeUnitDeclaration: declaration,
			})
			break
		}
		if language := nodeSourceLanguage(entry.Name()); language != "" {
			directory := filepath.ToSlash(filepath.Dir(filepath.ToSlash(relative)))
			if directory == "" {
				directory = "."
			}
			nodeSources = append(nodeSources, nodeSourceEvidence{directory: directory, language: language})
		}
		if isPythonSource(entry.Name()) {
			directory := filepath.ToSlash(filepath.Dir(filepath.ToSlash(relative)))
			if directory == "" {
				directory = "."
			}
			pythonSourceDirectories = append(pythonSourceDirectories, directory)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	declarations = retainSourceBackedDeclarations(declarations, pythonSourceDirectories)
	resolveNodeDeclarationLanguages(declarations, nodeSources)
	return declarations, nil
}

// retainSourceBackedDeclarations prevents dependency-only fixture trees from
// becoming runtime boundaries. Root dependency manifests remain sufficient for
// the common requirements-only repository, and a weak manifest co-located with
// a strong declaration remains part of that unit's complete manifest evidence.
func retainSourceBackedDeclarations(declarations []discoveredDeclaration, pythonSourceDirectories []string) []discoveredDeclaration {
	strongPythonDirectories := make(map[string]struct{})
	for _, declaration := range declarations {
		if declaration.language == "python" && !declaration.requiresSource {
			strongPythonDirectories[declaration.directory] = struct{}{}
		}
	}
	retained := declarations[:0]
	for _, declaration := range declarations {
		if !declaration.requiresSource || declaration.directory == "." {
			retained = append(retained, declaration)
			continue
		}
		if _, strong := strongPythonDirectories[declaration.directory]; strong {
			retained = append(retained, declaration)
			continue
		}
		for _, sourceDirectory := range pythonSourceDirectories {
			if isRelativeWithin(declaration.directory, sourceDirectory) {
				retained = append(retained, declaration)
				break
			}
		}
	}
	return retained
}

func isPythonSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".py", ".pyi", ".pyw":
		return true
	default:
		return false
	}
}

// resolveNodeDeclarationLanguages keeps package.json as the structural unit
// marker while deriving the unit's actual source language from the same
// filesystem walk. Source evidence belongs to the nearest package boundary,
// so a nested JavaScript package does not misclassify its TypeScript parent.
// TypeScript wins for genuinely mixed units; a manifest with no source
// evidence preserves the registry's TypeScript fallback.
func resolveNodeDeclarationLanguages(declarations []discoveredDeclaration, sources []nodeSourceEvidence) {
	evidence := make(map[int]nodeLanguageEvidence)
	for _, source := range sources {
		owner := -1
		ownerDepth := -1
		for i := range declarations {
			declaration := declarations[i]
			if !isNodePackageDeclaration(declaration) || !isRelativeWithin(declaration.directory, source.directory) {
				continue
			}
			depth := relativePathDepth(declaration.directory)
			if depth > ownerDepth {
				owner = i
				ownerDepth = depth
			}
		}
		if owner < 0 {
			continue
		}
		observed := evidence[owner]
		switch source.language {
		case "typescript":
			observed.typescript = true
		case "javascript":
			observed.javascript = true
		}
		evidence[owner] = observed
	}
	for i, observed := range evidence {
		switch {
		case observed.typescript:
			declarations[i].language = "typescript"
		case observed.javascript:
			declarations[i].language = "javascript"
		}
	}
}

func isNodePackageDeclaration(declaration discoveredDeclaration) bool {
	return strings.EqualFold(filepath.Base(filepath.FromSlash(declaration.manifest)), "package.json")
}

func nodeSourceLanguage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	default:
		return ""
	}
}

func relativePathDepth(path string) int {
	if path == "." {
		return 0
	}
	return strings.Count(path, "/") + 1
}

func isRelativeWithin(parent, child string) bool {
	if parent == "." {
		return true
	}
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func skipCodeUnitDiscoveryDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "target", "dist", "build", "__pycache__", "venv":
		return true
	default:
		return false
	}
}

// assembleCodeUnits groups co-located declarations and solution-owned .NET
// projects without suppressing unsupported ecosystems.
func assembleCodeUnits(sourceDir string, declarations []discoveredDeclaration) []*codev0.CodeUnitInfo {
	if len(declarations) == 0 {
		return []*codev0.CodeUnitInfo{{
			Path: ".", Name: filepath.Base(filepath.Clean(sourceDir)),
			PrimaryLanguage: "unknown", Languages: []string{"unknown"}, RuntimeAgent: "generic",
		}}
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i].directory != declarations[j].directory {
			return declarations[i].directory < declarations[j].directory
		}
		if declarations[i].priority != declarations[j].priority {
			return declarations[i].priority < declarations[j].priority
		}
		return declarations[i].manifest < declarations[j].manifest
	})

	var aggregators []discoveredDeclaration
	for _, declaration := range declarations {
		if declaration.aggregatesTree {
			aggregators = append(aggregators, declaration)
		}
	}
	groups := make(map[string]*codeUnitAccumulator)
	for _, declaration := range declarations {
		unitPath := aggregateCodeUnitPath(declaration, aggregators)
		group := groups[unitPath]
		if group == nil {
			group = &codeUnitAccumulator{
				path: unitPath, primaryPriority: int(^uint(0) >> 1),
				languages: make(map[string]struct{}), manifests: make(map[string]struct{}), runtimeAgents: make(map[string]struct{}),
			}
			groups[unitPath] = group
		}
		group.languages[declaration.language] = struct{}{}
		group.manifests[declaration.manifest] = struct{}{}
		group.runtimeAgents[declaration.runtimeAgent] = struct{}{}
		if declaration.priority < group.primaryPriority {
			group.primaryPriority = declaration.priority
			group.primaryLanguage = declaration.language
		}
	}

	paths := make([]string, 0, len(groups))
	for path := range groups {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	units := make([]*codev0.CodeUnitInfo, 0, len(paths))
	for _, path := range paths {
		group := groups[path]
		languages := sortedSet(group.languages)
		manifests := sortedSet(group.manifests)
		runtimeAgent := "generic"
		if len(group.runtimeAgents) == 1 {
			for agent := range group.runtimeAgents {
				runtimeAgent = agent
			}
		}
		name := filepath.Base(filepath.FromSlash(path))
		if path == "." {
			name = filepath.Base(filepath.Clean(sourceDir))
		}
		units = append(units, &codev0.CodeUnitInfo{
			Path: path, Name: name, PrimaryLanguage: group.primaryLanguage,
			Languages: languages, ManifestPaths: manifests, RuntimeAgent: runtimeAgent,
		})
	}
	return units
}

func aggregateCodeUnitPath(declaration discoveredDeclaration, aggregators []discoveredDeclaration) string {
	best := declaration.directory
	bestDepth := -1
	for _, aggregator := range aggregators {
		if aggregator.language != declaration.language || aggregator.directory == declaration.directory {
			continue
		}
		if !isRelativeDescendant(aggregator.directory, declaration.directory) {
			continue
		}
		if depth := strings.Count(aggregator.directory, "/"); depth > bestDepth {
			best = aggregator.directory
			bestDepth = depth
		}
	}
	return best
}

func isRelativeDescendant(parent, child string) bool {
	if parent == "." {
		return child != "."
	}
	return strings.HasPrefix(child, parent+"/")
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
