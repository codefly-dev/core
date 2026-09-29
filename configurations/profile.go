package configurations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// ProfileDerivationFile is the file a profile directory carries to declare the
// profile whose groups it starts from. It is named like every other declaration
// Codefly reads, so the group walker — which skips ".codefly." files — never
// mistakes it for a configuration group.
const ProfileDerivationFile = "profile.codefly.yaml"

// ProfileValueMarker is what a configuration value holds instead of a value to
// declare that each profile supplies its own. A value still carrying it when the
// selected profile and everything it derives from have been read is not a value:
// the render fails naming it, rather than delivering "${profile}" or, worse,
// inheriting the base profile's development value into a deployed environment.
//
// It is the declaration that makes an environment-specific difference reviewable
// in one place: the group is declared once, and the keys that MUST differ per
// environment say so where they are declared rather than in each profile that
// remembers to restate them.
const ProfileValueMarker = "${profile}"

// maxProfileDerivationDepth bounds the derivation walk. A chain deeper than this
// is a modelling mistake rather than a composition, and the bound is what keeps a
// malformed tree from being read forever.
const maxProfileDerivationDepth = 8

// ProfileDerivation is one profile directory's declaration of what it derives
// from. Fields are strict: an unknown key is a typo that would otherwise leave
// the derivation silently unread, which is the failure mode this file exists to
// remove.
type ProfileDerivation struct {
	// DerivesFrom names the profile, at the same configuration location, whose
	// groups this profile starts from. This profile then supplies only the
	// values that differ.
	DerivesFrom string `yaml:"derives-from"`
}

// ErrProfileDerivation marks a derivation that cannot be honoured: a profile
// deriving from one that is not there, a cycle, or a chain past the depth bound.
var ErrProfileDerivation = errors.New("configuration profile derivation cannot be resolved")

// ProfileRequirement is one configuration value a group declares as supplied per
// profile (ProfileValueMarker) that the selected profile did not supply.
type ProfileRequirement struct {
	// Origin is the service unique the group belongs to, empty for a workspace
	// configuration.
	Origin string
	// Group and Key locate the value.
	Group string
	Key   string
	// Profile is the profile that was selected at this location, and DeclaredIn
	// the directory of the layer that left the marker standing — which is where a
	// reader sees the declaration, and is not always the selected profile's own.
	Profile    string
	DeclaredIn string
}

func (requirement ProfileRequirement) String() string {
	origin := requirement.Origin
	if origin == "" {
		origin = resources.ConfigurationWorkspace
	}
	return fmt.Sprintf("%s: %s/%s is supplied per profile (declared in %s) and profile %q supplies no value",
		origin, requirement.Group, requirement.Key, requirement.DeclaredIn, requirement.Profile)
}

// UnsuppliedProfileValuesError lists every value the selected profile owes at
// once, so a composition supplies them in one pass rather than one failed render
// at a time.
type UnsuppliedProfileValuesError struct {
	Requirements []ProfileRequirement
}

func (e *UnsuppliedProfileValuesError) Error() string {
	lines := make([]string, 0, len(e.Requirements)+1)
	lines = append(lines, fmt.Sprintf("%d configuration value(s) are supplied per profile and the selected profile supplies none:", len(e.Requirements)))
	for _, requirement := range e.Requirements {
		lines = append(lines, "  - "+requirement.String())
	}
	return strings.Join(lines, "\n")
}

// ProfileLayers returns the directories one configuration location is read from
// for the selected profile, BASE FIRST and the selected profile last, and whether
// the location holds the profile at all.
//
// The selected profile is the one ProfileDirectory picks: the first profile of
// the environment's chain this location holds. From there, each profile
// directory's ProfileDerivationFile names the profile it derives from, and that
// profile's directory at the SAME location is read before it. So a location
// holding configurations/shared and configurations/deployed, where deployed
// derives from shared, is read as [shared, deployed] — one declared set of groups
// with the deployed differences layered on top as values.
//
// Derivation is declared by the profile's author, beside the values, and the
// environment's chain is declared by the workspace that consumes it: the two do
// not overlap. A profile named in a derivation must exist at that location — a
// derivation that cannot be honoured is an error, never a silent read of the
// undermost profile alone, which would hand a deployed environment whatever the
// author expected to be overridden.
func ProfileLayers(ctx context.Context, base, kind string, profiles []string) ([]string, bool, error) {
	selected, exists, err := ProfileDirectory(ctx, base, kind, profiles)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	layers := []string{selected}
	seen := map[string]bool{path.Base(selected): true}
	for current := selected; ; {
		derivation, err := readProfileDerivation(ctx, current)
		if err != nil {
			return nil, false, err
		}
		if derivation == nil || strings.TrimSpace(derivation.DerivesFrom) == "" {
			break
		}
		name := strings.TrimSpace(derivation.DerivesFrom)
		if err := resources.ValidateConfigurationProfileName(name); err != nil {
			return nil, false, fmt.Errorf("%s: %w: %w", path.Join(current, ProfileDerivationFile), err, ErrProfileDerivation)
		}
		if seen[name] {
			return nil, false, fmt.Errorf("configuration profile %q at %s derives from %q, which is already in the chain: %w",
				path.Base(current), path.Join(base, kind), name, ErrProfileDerivation)
		}
		if len(layers) > maxProfileDerivationDepth {
			return nil, false, fmt.Errorf("configuration profile %q at %s derives through more than %d profiles: %w",
				path.Base(selected), path.Join(base, kind), maxProfileDerivationDepth, ErrProfileDerivation)
		}
		dir := path.Join(base, kind, name)
		derivedExists, err := shared.DirectoryExists(ctx, dir)
		if err != nil {
			return nil, false, err
		}
		if !derivedExists {
			return nil, false, fmt.Errorf("configuration profile %q at %s derives from %q, which has no directory there: %w",
				path.Base(current), path.Join(base, kind), name, ErrProfileDerivation)
		}
		seen[name] = true
		layers = append(layers, dir)
		current = dir
	}
	// Collected most derived first; a reader overlays base first.
	for i, j := 0, len(layers)-1; i < j; i, j = i+1, j-1 {
		layers[i], layers[j] = layers[j], layers[i]
	}
	return layers, true, nil
}

// readProfileDerivation reads a profile directory's derivation declaration, or
// nil when it declares none.
func readProfileDerivation(ctx context.Context, dir string) (*ProfileDerivation, error) {
	file := path.Join(dir, ProfileDerivationFile)
	exists, err := shared.FileExists(ctx, file)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	decoder.KnownFields(true)
	derivation := &ProfileDerivation{}
	if err := decoder.Decode(derivation); err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", file, err)
	}
	return derivation, nil
}

// ProfileFile returns the path of a single-file declaration — a service's
// dns.codefly.yaml — in the MOST DERIVED layer that holds it, and whether any
// layer does. A file has no keys to overlay, so the derived profile's copy
// replaces the one it derives from, and a derived profile that ships none keeps
// the declaration it derives from instead of silently having none.
func ProfileFile(ctx context.Context, base, kind string, profiles []string, name string) (string, bool, error) {
	layers, exists, err := ProfileLayers(ctx, base, kind, profiles)
	if err != nil {
		return "", false, err
	}
	if !exists {
		dir, _, err := ProfileDirectory(ctx, base, kind, profiles)
		if err != nil {
			return "", false, err
		}
		return path.Join(dir, name), false, nil
	}
	for i := len(layers) - 1; i >= 0; i-- {
		candidate := path.Join(layers[i], name)
		held, err := shared.FileExists(ctx, candidate)
		if err != nil {
			return "", false, err
		}
		if held {
			return candidate, true, nil
		}
	}
	return path.Join(layers[len(layers)-1], name), false, nil
}

// ProfileConfigurations is what one configuration location provides for the
// selected profile: the groups of the profile it derives from with this
// profile's values layered over them, and the values still owed.
type ProfileConfigurations struct {
	// Infos are the overlaid groups, in the order the base layer declared them,
	// with groups a derived layer introduces appended.
	Infos []*basev0.ConfigurationInformation
	// Exists reports whether the location holds the selected profile at all.
	Exists bool
	// Unsupplied are the values declared as supplied per profile that no layer
	// supplied. They are RETURNED rather than failed here so a caller can let an
	// invocation-scoped override supply one before deciding; a load that does not
	// must fail on them.
	Unsupplied []ProfileRequirement
	// Layers are the directories read, base first.
	Layers []string
}

// LoadProfileConfigurations reads one configuration location for the selected
// profile: every layer of ProfileLayers, overlaid base first so a derived
// profile's file supplies only what differs. Within one layer nothing changes —
// files are read and consolidated exactly as before.
//
// Overlay is per VALUE, matched the way every other configuration lookup matches
// (resources.Match: case insensitively, with "-" and "_" equivalent), so a
// derived profile restates one key rather than a whole group. A group only a
// derived profile declares is appended; a group only the base declares is
// inherited whole. A structured document (a .yaml group) has no keys to overlay,
// so a derived profile's document replaces the one it derives from, and a layer
// that would turn a document into key/value pairs, or the reverse, is a conflict
// rather than a silent choice between them.
func LoadProfileConfigurations(ctx context.Context, base, kind string, profiles []string) (*ProfileConfigurations, error) {
	w := wool.Get(ctx).In("configurations.LoadProfileConfigurations")
	layers, exists, err := ProfileLayers(ctx, base, kind, profiles)
	if err != nil {
		return nil, err
	}
	out := &ProfileConfigurations{Exists: exists, Layers: layers}
	if !exists {
		return out, nil
	}
	overlay := newProfileOverlay()
	for _, layer := range layers {
		loaded, err := LoadConfigurationInformationsFromFiles(ctx, layer)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load configurations from profile %s", path.Base(layer))
		}
		if err := overlay.add(layer, loaded); err != nil {
			return nil, err
		}
	}
	out.Infos = overlay.infos
	out.Unsupplied = overlay.unsupplied(path.Base(layers[len(layers)-1]))
	return out, nil
}

// profileOverlay accumulates the layers of one location, newest last, and
// remembers which layer last wrote each value so an unsupplied one can name the
// file a reader must look at.
type profileOverlay struct {
	infos []*basev0.ConfigurationInformation
	// writtenIn maps a value to the layer directory that last set it.
	writtenIn map[string]string
}

func newProfileOverlay() *profileOverlay {
	return &profileOverlay{writtenIn: make(map[string]string)}
}

func profileValueKey(group, key string) string {
	return strings.ToLower(strings.ReplaceAll(group, "-", "_")) + "\x00" + strings.ToLower(strings.ReplaceAll(key, "-", "_"))
}

func (overlay *profileOverlay) add(layer string, infos []*basev0.ConfigurationInformation) error {
	for _, info := range infos {
		if info == nil {
			continue
		}
		existing := overlay.find(info.GetName())
		if existing == nil {
			overlay.infos = append(overlay.infos, info)
			for _, value := range info.GetConfigurationValues() {
				overlay.writtenIn[profileValueKey(info.GetName(), value.GetKey())] = layer
			}
			continue
		}
		if (info.GetData() != nil) != (existing.GetData() != nil) {
			return fmt.Errorf("configuration %q is a structured document in one profile and key/value pairs in %q: %w",
				info.GetName(), path.Base(layer), ErrConfigurationConflict)
		}
		if info.GetData() != nil {
			existing.Data = info.GetData()
			continue
		}
		for _, value := range info.GetConfigurationValues() {
			overlay.set(layer, existing, value)
		}
	}
	return nil
}

func (overlay *profileOverlay) find(name string) *basev0.ConfigurationInformation {
	for _, info := range overlay.infos {
		if resources.Match(info.GetName(), name) {
			return info
		}
	}
	return nil
}

func (overlay *profileOverlay) set(layer string, info *basev0.ConfigurationInformation, value *basev0.ConfigurationValue) {
	overlay.writtenIn[profileValueKey(info.GetName(), value.GetKey())] = layer
	for index, existing := range info.ConfigurationValues {
		if resources.Match(existing.GetKey(), value.GetKey()) {
			// The derived layer's declaration replaces the whole value, not only
			// its text: a profile that turns a plaintext default into a secret,
			// or into an assembled template, says so here and a merge of the two
			// would deliver half of each.
			info.ConfigurationValues[index] = value
			return
		}
	}
	info.ConfigurationValues = append(info.ConfigurationValues, value)
}

// unsupplied lists the values still carrying the marker once every layer is in.
// The marker is looked for anywhere in the value, not only as the whole of it, so
// a partially written declaration ("https://${profile}/token") is owed too rather
// than shipped as an address. A structured document is not scanned: its content
// is an opaque blob to Codefly, and reporting a marker inside one would claim a
// key/value model it does not have.
func (overlay *profileOverlay) unsupplied(profile string) []ProfileRequirement {
	var out []ProfileRequirement
	for _, info := range overlay.infos {
		for _, value := range info.GetConfigurationValues() {
			if !ValueDeclaredPerProfile(value) {
				continue
			}
			out = append(out, ProfileRequirement{
				Group:      info.GetName(),
				Key:        value.GetKey(),
				Profile:    profile,
				DeclaredIn: overlay.writtenIn[profileValueKey(info.GetName(), value.GetKey())],
			})
		}
	}
	return out
}

// ValueDeclaredPerProfile reports whether a configuration value still declares
// that a profile owes it. A templated value is judged by its literals, where a
// producer writes text, for the same reason endpoint references are.
func ValueDeclaredPerProfile(value *basev0.ConfigurationValue) bool {
	if value == nil {
		return false
	}
	if strings.Contains(value.GetValue(), ProfileValueMarker) {
		return true
	}
	for _, segment := range value.GetTemplate().GetSegments() {
		if strings.Contains(segment.GetLiteral(), ProfileValueMarker) {
			return true
		}
	}
	return false
}

// StillUnsupplied narrows requirements to those a value in the final set still
// owes, keyed by origin. It is what lets an invocation-scoped override supply a
// per-profile value: the requirement is collected when the profile is read and
// discharged when the override lands, so `--set` is a way to supply one and not a
// way around the declaration.
func StillUnsupplied(requirements []ProfileRequirement, byOrigin func(origin string) []*basev0.ConfigurationInformation) []ProfileRequirement {
	var out []ProfileRequirement
	// One value is owed once, however many places offered the group. Two composed
	// modules vendoring the same file both collect the requirement — identical
	// definitions are deliberately not a conflict — and listing it twice would
	// read as two problems to fix.
	seen := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		key := requirement.Origin + "\x00" + profileValueKey(requirement.Group, requirement.Key)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !stillCarriesMarker(byOrigin(requirement.Origin), requirement.Group, requirement.Key) {
			continue
		}
		out = append(out, requirement)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i], out[j]
		return left.Origin+"\x00"+left.Group+"\x00"+left.Key < right.Origin+"\x00"+right.Group+"\x00"+right.Key
	})
	return out
}

func stillCarriesMarker(infos []*basev0.ConfigurationInformation, group, key string) bool {
	for _, info := range infos {
		if !resources.Match(info.GetName(), group) {
			continue
		}
		for _, value := range info.GetConfigurationValues() {
			if resources.Match(value.GetKey(), key) {
				return ValueDeclaredPerProfile(value)
			}
		}
	}
	// The group or key is gone from the final set — dropped by composition, or
	// never provisioned. Nothing delivers the marker, so nothing is owed.
	return false
}
