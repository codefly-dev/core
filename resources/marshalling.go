package resources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/codefly-dev/core/internal/wire"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"
	"gopkg.in/yaml.v3"
)

// ErrInvalidManifestWireForm is the one sentinel for a manifest refused on its
// YAML tree, before any field of it is read: a duplicate key, a merge key, an
// explicit null, a fractional number, a key that is not a name. These are
// properties of the DOCUMENT, so the refusal names no field and no model —
// a caller that wants to know which rule fired reads the message, and a caller
// that wants to know the manifest is unloadable matches this.
var ErrInvalidManifestWireForm = errors.New("the manifest's wire form is invalid")

// An inline host map must not override a field owned by the resource schema.
// yaml.v3 panics for this programmer error; return an ordinary save error.
func validateExtensionKeys(resource any, extensions map[string]YAMLValue) error {
	typ := reflect.TypeOf(resource)
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, exists := extensions[name]; exists {
			return fmt.Errorf("host extension %q conflicts with a resource field", name)
		}
	}
	return nil
}

func TypeName[C Configuration]() string {
	var c C
	return fmt.Sprintf("%T", c)
}

func LoadFromFs[C Configuration](fs shared.FileSystem) (*C, error) {
	w := wool.Get(context.Background()).In("configurations.LoadFromFs", wool.Field("type", TypeName[C]()))
	content, err := fs.ReadFile(ConfigurationFile[C]())
	if err != nil {
		return nil, w.Wrapf(err, "cannot read file")
	}
	conf, err := LoadFromBytes[C](content)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load from bytes")
	}
	return conf, nil
}

func LoadFromDir[C Configuration](ctx context.Context, dir string) (*C, error) {
	w := wool.Get(ctx).In("configurations.LoadFromDir")
	p, err := Path[C](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	exists, err := shared.FileExists(ctx, p)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !exists {
		return nil, shared.NewErrorResourceNotFound(TypeName[C](), p)
	}
	return LoadFromPath[C](ctx, p)
}

func LoadFromPath[C Configuration](ctx context.Context, p string) (*C, error) {
	w := wool.Get(ctx).In("configurations.LoadWorkspace", wool.Field("path", p))
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, w.NewError("path doesn't exist <%s>", p)
	}
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, w.NewError("cannot read path %s: %s", p, err)
	}
	return LoadFromBytes[C](content)
}

// LoadFromBytes decodes one configuration document. A refusal the decoder makes
// — an endpoint declaration refused by its keys, for one — is wrapped, not
// flattened, so a caller can still tell it by its sentinel.
func LoadFromBytes[C Configuration](content []byte) (*C, error) {
	var config C
	if !closedSchema[C]() {
		if err := yaml.Unmarshal(content, &config); err != nil {
			return nil, fmt.Errorf("cannot unmarshal service configuration: %w", err)
		}
		return &config, nil
	}
	// A closed schema: every key the file carries is one the type declares,
	// or the load fails naming it. The TREE is held to the node-level checks
	// the wire documents share first (internal/wire), because the typed
	// decoder repairs what those refuse — a null reads as "no declaration", a
	// merge key carries a mapping in after every check — then the kind is
	// dispatched and the keys an older layout wrote are named with their
	// remedy, and only then does yaml's own KnownFields read the fields; a key
	// it does not know is never dropped on the way to the model.
	if err := checkManifestNodes[C](content); err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("cannot unmarshal %s configuration: a key no %s manifest declares, or a value of the wrong shape: %w", TypeName[C](), TypeName[C](), err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("cannot unmarshal %s configuration: the file holds more than one document", TypeName[C]())
		}
		return nil, fmt.Errorf("cannot unmarshal %s configuration: %w", TypeName[C](), err)
	}
	return &config, nil
}

// closedSchema reports whether a configuration type admits only the keys it
// declares, so that a key the loader does not know is refused rather than
// dropped.
//
// Module is closed. Its manifest declares `vendored:`, the input to a build
// fact the presence document signs (solutionhost.BuildSize): a declaration a
// lenient loader dropped — `vendorred:`, a tab for a space — would make the
// count include the kit while the document truthfully said nothing was
// excluded, and nothing would say so. The one other document that shares the
// file name, the composition descriptor (`kind: composed-module`,
// composition.Descriptor), has its own strict loader and is told apart by its
// kind, which postLoad refuses here by name.
//
// The other types stay as they are: Workspace, Service and Environment carry
// an inline extension map by design, and the rest are not this change's to
// survey.
func closedSchema[C Configuration]() bool {
	var c C
	switch any(c).(type) {
	case Module:
		return true
	}
	return false
}

func SaveToDir[C Configuration](ctx context.Context, c *C, dir string) error {
	return saveToDir(ctx, c, dir, false)
}

// SaveToDirKeepingComments saves like SaveToDir, but when the file already
// exists its comments, key order and scalar quoting are carried onto the new
// content (see marshalKeepingComments). A new file, or one with no comments, is
// written byte for byte as SaveToDir writes it.
func SaveToDirKeepingComments[C Configuration](ctx context.Context, c *C, dir string) error {
	return saveToDir(ctx, c, dir, true)
}

func saveToDir[C Configuration](ctx context.Context, c *C, dir string, keepComments bool) error {
	w := wool.Get(ctx).In("SaveToDir[%s]", wool.GenericField[C](), wool.DirField(dir))
	w.Trace("saving")
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return w.Wrapf(err, "cannot check directory")
	}
	file, err := Path[C](ctx, dir)
	if err != nil {
		return w.Wrapf(err, "cannot get path")
	}
	exists, err := shared.FileExists(ctx, file)
	if err != nil {
		return w.Wrapf(err, "cannot check file existence")
	}
	if exists {
		override := shared.GetOverride(ctx)
		if !override.Replace(file) {
			w.Debug("file already exists without override: skipping", wool.FileField(file))
			return nil
		}
	}
	var existing []byte
	if exists && keepComments {
		existing, err = os.ReadFile(file)
		if err != nil {
			return w.Wrapf(err, "cannot read existing file")
		}
	}
	content, err := marshalKeepingComments(existing, *c)
	if err != nil {
		return w.Wrapf(err, "cannot marshal")
	}
	err = shared.WriteFileAtomic(ctx, file, content, 0o600)
	if err != nil {
		return w.Wrapf(err, "cannot write")
	}
	return nil
}

// FindUp looks for a configuration in the active directory and up
func FindUp[C Configuration](ctx context.Context) (*string, error) {
	w := wool.Get(ctx).In("configurations.FindUp", wool.GenericField[C]())
	cur, err := os.Getwd()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get active directory")
	}
	return FindUpFrom[C](ctx, cur)
}

// FindUpFrom looks for a configuration in dir and up. Callers that must not
// depend on the process working directory — a long-lived session anchored to
// the directory it was created in — pass their own absolute directory.
func FindUpFrom[C Configuration](ctx context.Context, dir string) (*string, error) {
	w := wool.Get(ctx).In("configurations.FindUpFrom", wool.GenericField[C](), wool.DirField(dir))
	cur := dir
	var atRoot bool
	for {
		// Look for a configuration
		p, err := Path[C](ctx, cur)
		if err != nil {
			return nil, w.Wrapf(err, "cannot get path")
		}
		if _, err := os.Stat(p); err == nil {
			w.Trace("found", wool.DirField(p))
			return &cur, nil
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			if atRoot {
				return nil, nil
			}
			atRoot = true
		}
	}
}

// vestigialModuleKeys are top-level keys that module manifests on disk carry
// and that no module manifest has ever declared: an earlier workspace layout
// wrote them, nothing read them, and the lenient loader dropped them without
// a word. The strict loader refuses them like any unknown key, but names them
// here with the remedy, because a workspace that loaded yesterday and does
// not today deserves to be told why in one line and what to do about it.
var vestigialModuleKeys = []string{"domain", "project"}

// checkManifestNodes holds a closed-schema manifest's YAML tree to the checks
// that must run before the typed decoder reads it: the node-level rules every
// wire document shares, the manifest's kind, and the vestigial keys. Only the
// module manifest is closed today; the others return at once.
func checkManifestNodes[C Configuration](content []byte) error {
	var c C
	if _, isModule := any(c).(Module); !isModule {
		return nil
	}
	var tree yaml.Node
	if err := yaml.Unmarshal(content, &tree); err != nil {
		return nil // the typed decoder reports what is wrong with the document
	}
	label := TypeName[C]()
	switch defect := wire.Check(&tree); defect.Kind {
	case wire.DuplicateKey:
		return fmt.Errorf("%w: cannot unmarshal %s configuration: the mapping at %s names the key %q twice; a repeated key — including one written through an alias — makes the loaded manifest depend on order", ErrInvalidManifestWireForm, label, defect.Path, defect.Detail)
	case wire.MergeKey:
		return fmt.Errorf("%w: cannot unmarshal %s configuration: the mapping at %s uses the merge key <<; yaml applies a merge inside its typed decoder, after every check over the tree, so a declaration carried in through one — a vendored list, a service — would shadow or be shadowed unseen; no field of a module manifest is declared by merging", ErrInvalidManifestWireForm, label, defect.Path)
	case wire.NullNode:
		consequence := "a null reads as \"nothing declared\" to the typed decoder"
		if strings.HasPrefix(defect.Path, "vendored") {
			consequence += ", so a vendored list written as null would count the kit while the manifest said it was excluded"
		}
		return fmt.Errorf("%w: cannot unmarshal %s configuration: the manifest carries an explicit null at %s %s; %s — an absent key is absent, and an empty list is written []", ErrInvalidManifestWireForm, label, defect.Path, defect.Detail, consequence)
	case wire.FractionalNumber:
		return fmt.Errorf("%w: cannot unmarshal %s configuration: the number %s at %s is not a whole number; no field of a module manifest takes a fraction", ErrInvalidManifestWireForm, label, defect.Detail, defect.Path)
	case wire.KeyNotAName:
		return fmt.Errorf("%w: cannot unmarshal %s configuration: the mapping at %s carries a key that is not a name (%q)", ErrInvalidManifestWireForm, label, defect.Path, defect.Detail)
	}
	root := tree.Content
	if tree.Kind != yaml.DocumentNode || len(root) == 0 || root[0].Kind != yaml.MappingNode {
		return nil
	}
	mapping := root[0]
	// The kind, dispatched from the node before the typed decoder reads a
	// field: a composition descriptor shares this file name and carries keys
	// the module model does not declare, so a strict decode would refuse it
	// for its first unknown key and never say what it is. Named here first.
	// Key AND value are RESOLVED through the one wire helper, so what is
	// dispatched is what the typed decoder will read: a kind written as an
	// alias to a string anchored elsewhere (`description: &k composed-module`,
	// `kind: *k`) is the descriptor's kind, and a kind that resolves to
	// anything but a plain string is no kind at all.
	var found []string
	seen := map[string]bool{}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := wire.Resolve(mapping.Content[index])
		if key == nil || key.Kind != yaml.ScalarNode {
			continue // wire.Check has already refused a key that is not a name
		}
		if key.Value == "kind" {
			value := wire.Resolve(mapping.Content[index+1])
			if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("cannot unmarshal %s configuration: %s declares a kind that is not a plain string; a module manifest declares kind %q", label, ModuleConfigurationName, ModuleKind)
			}
			switch value.Value {
			case "", ModuleKind:
			case compositionDescriptorKind:
				return fmt.Errorf("cannot unmarshal %s configuration: %s is a composition descriptor (kind %q), not a module manifest; read it with composition.LoadDescriptor", label, ModuleConfigurationName, value.Value)
			default:
				return fmt.Errorf("cannot unmarshal %s configuration: %s declares kind %q; a module manifest declares kind %q", label, ModuleConfigurationName, value.Value, ModuleKind)
			}
		}
		if slices.Contains(vestigialModuleKeys, key.Value) && !seen[key.Value] {
			seen[key.Value] = true
			found = append(found, fmt.Sprintf("%s (line %d)", key.Value, key.Line))
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("cannot unmarshal %s configuration: %s carries %s, keys an earlier workspace layout wrote that no module manifest declares and nothing reads; this loader no longer drops a key it does not know, so delete them and the manifest loads as before",
		label, ModuleConfigurationName, strings.Join(found, " and "))
}
