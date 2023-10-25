package configurations

//
//import (
//	"fmt"
//	"github.com/codefly-dev/core/shared"
//	"path"
//	"slices"
//	"strings"
//)
//
//const LibraryConfigurationName = "library.codefly.yaml"
//const LibraryGenerationConfigurationName = "library.generation.codefly.yaml"
//
///*
//Convention: Relative NewDir is the path from the root of the project to the library directory
//Configuration file is located one up by current standard
//// TODO: May change the default or make it configurable
//*/
//
//type Library struct {
//	Kind         string  `yaml:"kind"`
//	Plugin       *Plugin `yaml:"plugin"`
//	RelativePath string  `yaml:"relative-path"`
//}
//
//func (l *Library) ImplementationKind() string {
//	if !slices.Contains(KnownLibraryKinds(), l.Kind) {
//		shared.Exit("unknown plugin kind: %s", l.Kind)
//	}
//	return l.Kind
//}
//
//func KnownLibraryKinds() []string {
//	return []string{"go"}
//}
//
//func (l *Library) Path() (string, error) {
//	return path.Join(GlobalConfigurationDir(), "plugins", "libraries", l.Kind, l.Plugin.Name()), nil
//}
//
//func (l *Library) Name() string {
//	return fmt.Sprintf("%s/%s", l.Kind, l.Plugin.Name())
//}
//
//func (l *Library) Unique() string {
//	return fmt.Sprintf("%s/%s", l.Kind, l.Plugin.Unique())
//}
//
//func (l *Library) Localize(path string) {
//	l.RelativePath = path
//}
//
//func (l *Library) FullPath() string {
//	return path.Join(MustCurrentProject().dir(), l.RelativePath)
//}
//
//type LibraryGeneration struct {
//	Root          string               `yaml:"root"`
//	Exclude       []string             `yaml:"exclude"`
//	AutoGenerated []string             `yaml:"auto-generated"`
//	Extensions    []string             `yaml:"extensions"`
//	Pattern       string               `yaml:"pattern"`
//	Replacements  []shared.Replacement `yaml:"replacements"`
//}
//
//func ValidLibraryKinds() []string {
//	return []string{"go"}
//}
//
//func ParseLibrary(s string) (*Library, error) {
//	logger := shared.NewLogger("configurations.ParseLibrary")
//	tokens := strings.SplitN(s, "/", 2)
//	if len(tokens) != 2 {
//		return nil, logger.Errorf("invalid library format")
//	}
//	kind := tokens[0]
//	if !slices.Contains(ValidLibraryKinds(), kind) {
//		return nil, logger.Errorf("invalid library kind <%s>", kind)
//	}
//	plugin, err := ParsePlugin(PluginLibrary, tokens[1])
//	if err != nil {
//		return nil, logger.Wrapf(err, "cannot parse plugin")
//	}
//	return &Library{
//		Kind:   kind,
//		Plugin: plugin,
//	}, nil
//
//}
//
//func (g *LibraryGeneration) SaveToDir(dir string) error {
//	return SaveToDir(g, dir)
//}
//
//func LoadLibraryFromDir(dir string) (*Library, error) {
//	logger := shared.NewLogger("configurations.LoadLibraryFromDir<%s>", dir)
//	config, err := LoadFromDir[Library](dir)
//	if err != nil {
//		return nil, logger.Wrapf(err, "cannot load library configuration")
//	}
//	return config, nil
//}
//
//type LibrarySummary struct {
//	Kind  string         `yaml:"kind"`
//	Bases []*LibraryBase `yaml:"bases"`
//}
//
//type LibraryBase struct {
//	Name   string `yaml:"name"`
//	Usages []*LibraryUsage
//}
//
///*
//Convention: Relative NewDir from Project
//*/
//
//type LibraryUsage struct {
//	RelativePath string `yaml:"relative-path"`
//	Version      string `yaml:"version"`
//}
//
//type LibraryManager struct {
//	Current []*LibrarySummary
//}
//
//func NewLibraryManager(usages []*LibrarySummary) *LibraryManager {
//	return &LibraryManager{Current: usages}
//}
//
//func (m *LibraryManager) Summary(kind string) *LibrarySummary {
//	for _, l := range m.Current {
//		if l.Kind == kind {
//			return l
//		}
//	}
//	n := &LibrarySummary{Kind: kind}
//	m.Current = append(m.Current, n)
//	return n
//}
//
//func (m *LibraryManager) Add(lib *Library, relativePath string) error {
//	summary := m.Summary(lib.Kind)
//	base := summary.Find(lib.Plugin.Identifier)
//	for _, usage := range base.Usages {
//		if usage.RelativePath == relativePath && usage.Version == lib.Plugin.Version {
//			return nil
//		}
//	}
//
//	base.Usages = append(base.Usages, &LibraryUsage{
//		Version:      lib.Plugin.Version,
//		RelativePath: relativePath,
//	})
//	return nil
//}
//
//func (m *LibraryManager) ToSummary() []*LibrarySummary {
//	return m.Current
//}
//
//func (s *LibrarySummary) Find(base string) *LibraryBase {
//	for _, l := range s.Bases {
//		if l.Name == base {
//			return l
//		}
//	}
//	n := &LibraryBase{Name: base}
//	s.Bases = append(s.Bases, n)
//	return n
//}
