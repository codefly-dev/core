package semantic

// ARCHITECTURE: declarative project metadata is parsed inside the semantic
// analyzer because Gradle manifests need tree-sitter grammars. Codefly owns
// manifest and source parsing, while callers receive typed project evidence or
// a structured unsupported failure. No native build command is executed here.

import (
	"context"
	"encoding/xml"
	"fmt"
	osfs "io/fs"
	"path/filepath"
	"sort"
	"strings"

	tsgroovy "github.com/dekobon/tree-sitter-groovy/bindings/go"
	tskotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/codefly-dev/core/code"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// InspectProject fills declarative project metadata for a JVM or .NET code
// unit. Other languages are rejected: their inspection belongs to a dedicated
// language agent.
func (a *Analyzer) InspectProject(ctx context.Context, fs code.VFS, sourceDir, language string, info *codev0.GetProjectInfoResponse) error {
	switch language {
	case "jvm":
		return a.inspectJVMProject(ctx, fs, sourceDir, info)
	case "dotnet":
		return a.inspectDotNetProject(ctx, fs, sourceDir, info)
	default:
		return fmt.Errorf("project inspection is unavailable for language %q", language)
	}
}

func (a *Analyzer) inspectJVMProject(ctx context.Context, fs code.VFS, sourceDir string, info *codev0.GetProjectInfoResponse) error {
	if data, err := fs.ReadFile(filepath.Join(sourceDir, "pom.xml")); err == nil {
		if err := inspectMavenProject(data, info); err != nil {
			return fmt.Errorf("inspect pom.xml: %w", err)
		}
	} else if data, gradleErr := fs.ReadFile(filepath.Join(sourceDir, "build.gradle")); gradleErr == nil {
		if err := inspectGradleProject(ctx, data, sitter.NewLanguage(tsgroovy.Language()), info); err != nil {
			return fmt.Errorf("inspect build.gradle: %w", err)
		}
	} else if data, kotlinErr := fs.ReadFile(filepath.Join(sourceDir, "build.gradle.kts")); kotlinErr == nil {
		if err := inspectGradleProject(ctx, data, sitter.NewLanguage(tskotlin.Language()), info); err != nil {
			return fmt.Errorf("inspect build.gradle.kts: %w", err)
		}
	} else {
		return fmt.Errorf("no supported JVM manifest at code-unit root")
	}
	if info.Module == "" {
		for _, settingsName := range []string{"settings.gradle", "settings.gradle.kts"} {
			data, err := fs.ReadFile(filepath.Join(sourceDir, settingsName))
			if err != nil {
				continue
			}
			language := sitter.NewLanguage(tsgroovy.Language())
			if strings.HasSuffix(settingsName, ".kts") {
				language = sitter.NewLanguage(tskotlin.Language())
			}
			assignments, parseErr := syntaxAssignments(ctx, data, language)
			if parseErr != nil {
				return fmt.Errorf("inspect %s: %w", settingsName, parseErr)
			}
			info.Module = firstNonEmpty(assignments["rootProject.name"], assignments["rootProject.name "])
			break
		}
	}
	sourceFiles, err := a.SourceImports(ctx, fs, sourceDir, "jvm")
	if err != nil {
		return err
	}
	info.SourceFiles = sourceFiles
	info.Packages = packagesFromSourceFiles(sourceFiles)
	return nil
}

type mavenProject struct {
	GroupID      string            `xml:"groupId"`
	ArtifactID   string            `xml:"artifactId"`
	Version      string            `xml:"version"`
	Parent       mavenParent       `xml:"parent"`
	Properties   mavenProperties   `xml:"properties"`
	Dependencies []mavenDependency `xml:"dependencies>dependency"`
}

type mavenParent struct {
	GroupID string `xml:"groupId"`
	Version string `xml:"version"`
}

type mavenProperties struct {
	Values map[string]string
}

func (p *mavenProperties) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	p.Values = make(map[string]string)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			var body string
			if err := decoder.DecodeElement(&body, &value); err != nil {
				return err
			}
			p.Values[value.Name.Local] = strings.TrimSpace(body)
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

type mavenDependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func inspectMavenProject(data []byte, info *codev0.GetProjectInfoResponse) error {
	var project mavenProject
	if err := xml.Unmarshal(data, &project); err != nil {
		return err
	}
	group := firstNonEmpty(strings.TrimSpace(project.GroupID), strings.TrimSpace(project.Parent.GroupID))
	version := firstNonEmpty(strings.TrimSpace(project.Version), strings.TrimSpace(project.Parent.Version))
	if group != "" && strings.TrimSpace(project.ArtifactID) != "" {
		info.Module = group + ":" + strings.TrimSpace(project.ArtifactID)
	} else {
		info.Module = strings.TrimSpace(project.ArtifactID)
	}
	info.LanguageVersion = resolveManifestProperty(version, project.Properties.Values)
	for _, dependency := range project.Dependencies {
		name := strings.TrimSpace(dependency.ArtifactID)
		if dependency.GroupID != "" {
			name = strings.TrimSpace(dependency.GroupID) + ":" + name
		}
		if name == "" {
			continue
		}
		info.Dependencies = append(info.Dependencies, &codev0.Dependency{
			Name: name, Version: resolveManifestProperty(strings.TrimSpace(dependency.Version), project.Properties.Values), Direct: true,
		})
	}
	return nil
}

func inspectGradleProject(ctx context.Context, data []byte, language *sitter.Language, info *codev0.GetProjectInfoResponse) error {
	tree, err := parseSyntaxTree(ctx, language, data)
	if err != nil {
		return err
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return fmt.Errorf("syntax tree contains errors")
	}
	assignments, err := syntaxAssignmentsFromTree(root, data)
	if err != nil {
		return err
	}
	info.Module = firstNonEmpty(assignments["group"], assignments["rootProject.name"])
	compatibility := firstNonEmpty(assignments["sourceCompatibility"], assignments["targetCompatibility"])
	compatibility = strings.TrimPrefix(compatibility, "JavaVersion.VERSION_")
	info.LanguageVersion = compatibility
	variables := make(map[string]string)
	for key, value := range assignments {
		if !strings.Contains(key, ".") && value != "" {
			variables[key] = value
		}
	}
	seen := make(map[string]struct{})
	walkSyntax(root, func(node *sitter.Node) {
		if node.Kind() != "string" && node.Kind() != "string_literal" {
			return
		}
		if !insideNamedSyntaxBlock(node, data, "dependencies") {
			return
		}
		coordinate := resolveGradleInterpolation(firstStringLiteral(node, data), variables)
		name, version, ok := gradleCoordinate(coordinate)
		if !ok {
			return
		}
		key := name + "\x00" + version
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		info.Dependencies = append(info.Dependencies, &codev0.Dependency{Name: name, Version: version, Direct: true})
	})
	return nil
}

func syntaxAssignments(ctx context.Context, data []byte, language *sitter.Language) (map[string]string, error) {
	tree, err := parseSyntaxTree(ctx, language, data)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, fmt.Errorf("syntax tree contains errors")
	}
	return syntaxAssignmentsFromTree(tree.RootNode(), data)
}

func syntaxAssignmentsFromTree(root *sitter.Node, data []byte) (map[string]string, error) {
	assignments := make(map[string]string)
	walkSyntax(root, func(node *sitter.Node) {
		if node.Kind() != "assignment" && node.Kind() != "declaration" && node.Kind() != "assignment_expression" && node.Kind() != "local_variable_declaration" {
			return
		}
		text := strings.TrimSpace(node.Utf8Text(data))
		left, right, found := strings.Cut(text, "=")
		if !found {
			return
		}
		left = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(left), "def "))
		if fields := strings.Fields(left); len(fields) > 0 {
			left = fields[len(fields)-1]
		}
		right = strings.TrimSpace(right)
		value := firstStringLiteral(node, data)
		if value == "" {
			value = strings.TrimSuffix(strings.TrimSpace(right), ";")
		}
		if left != "" && value != "" {
			assignments[left] = value
		}
	})
	return assignments, nil
}

func insideNamedSyntaxBlock(node *sitter.Node, data []byte, name string) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case "function_call", "juxt_function_call", "call_expression":
			function := current.ChildByFieldName("function")
			if function != nil && strings.TrimSpace(function.Utf8Text(data)) == name {
				return true
			}
		case "command_chain":
			receiver := current.ChildByFieldName("receiver")
			if receiver != nil && strings.TrimSpace(receiver.Utf8Text(data)) == name {
				return true
			}
		}
	}
	return false
}

func resolveGradleInterpolation(value string, variables map[string]string) string {
	for key, replacement := range variables {
		value = strings.ReplaceAll(value, "${"+key+"}", replacement)
		value = strings.ReplaceAll(value, "$"+key, replacement)
	}
	return value
}

func gradleCoordinate(value string) (name, version string, ok bool) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	name = strings.TrimSpace(parts[0]) + ":" + strings.TrimSpace(parts[1])
	if len(parts) > 2 {
		version = strings.TrimSpace(strings.Join(parts[2:], ":"))
	}
	return name, version, true
}

func resolveManifestProperty(value string, properties map[string]string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		if replacement := properties[strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")]; replacement != "" {
			return replacement
		}
	}
	return value
}

func (a *Analyzer) inspectDotNetProject(ctx context.Context, fs code.VFS, sourceDir string, info *codev0.GetProjectInfoResponse) error {
	var manifests []string
	err := fs.WalkDir(sourceDir, func(filename string, entry osfs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filename != sourceDir && code.SkipInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".csproj":
			manifests = append(manifests, filename)
		case ".fsproj", ".vbproj":
			return fmt.Errorf("project manifest %q requires an owning language agent", filename)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(manifests) == 0 {
		return fmt.Errorf("no supported .NET project manifest at code-unit root")
	}
	sort.Strings(manifests)
	seen := make(map[string]struct{})
	frameworks := make(map[string]struct{})
	for _, filename := range manifests {
		data, err := fs.ReadFile(filename)
		if err != nil {
			return err
		}
		var project dotNetProject
		if err := xml.Unmarshal(data, &project); err != nil {
			return fmt.Errorf("inspect %s: %w", filename, err)
		}
		if info.Module == "" {
			info.Module = firstNonEmpty(strings.TrimSpace(project.AssemblyName), strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
		}
		if project.TargetFramework != "" {
			frameworks[strings.TrimSpace(project.TargetFramework)] = struct{}{}
		}
		for _, reference := range project.PackageReferences {
			name := strings.TrimSpace(reference.Include)
			if name == "" {
				continue
			}
			version := firstNonEmpty(strings.TrimSpace(reference.Version), strings.TrimSpace(reference.VersionElement))
			key := name + "\x00" + version
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			info.Dependencies = append(info.Dependencies, &codev0.Dependency{Name: name, Version: version, Direct: true})
		}
	}
	if len(frameworks) == 1 {
		for framework := range frameworks {
			info.LanguageVersion = framework
		}
	}
	sourceFiles, err := a.SourceImports(ctx, fs, sourceDir, "dotnet")
	if err != nil {
		return err
	}
	info.SourceFiles = sourceFiles
	info.Packages = packagesFromSourceFiles(sourceFiles)
	return nil
}

type dotNetProject struct {
	AssemblyName      string                   `xml:"PropertyGroup>AssemblyName"`
	TargetFramework   string                   `xml:"PropertyGroup>TargetFramework"`
	PackageReferences []dotNetPackageReference `xml:"ItemGroup>PackageReference"`
}

type dotNetPackageReference struct {
	Include        string `xml:"Include,attr"`
	Version        string `xml:"Version,attr"`
	VersionElement string `xml:"Version"`
}

func packagesFromSourceFiles(files []*codev0.SourceFileInfo) []*codev0.PackageInfo {
	type accumulator struct {
		files   []string
		imports []string
	}
	byPath := make(map[string]*accumulator)
	for _, file := range files {
		if file == nil {
			continue
		}
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(file.GetPath())))
		if directory == "" {
			directory = "."
		}
		if byPath[directory] == nil {
			byPath[directory] = &accumulator{}
		}
		byPath[directory].files = append(byPath[directory].files, filepath.Base(filepath.FromSlash(file.GetPath())))
		byPath[directory].imports = append(byPath[directory].imports, file.GetImports()...)
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]*codev0.PackageInfo, 0, len(paths))
	for _, path := range paths {
		value := byPath[path]
		sort.Strings(value.files)
		result = append(result, &codev0.PackageInfo{
			Name: path, RelativePath: path, Files: value.files, Imports: code.CanonicalImports(value.imports),
		})
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
