package resources

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/codefly-dev/core/internal/wire"
)

// The manifest's vendored-path declaration is the input to a signed build fact
// (solutionhost.BuildSize records the list a build excluded), so it is held to
// one spelling at the manifest and round-trips through save and load intact.
func TestModuleVendoredPathsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	module := &Module{
		Kind:              ModuleKind,
		Name:              "starter",
		ServiceReferences: []*ServiceReference{{Name: "api"}},
		Vendored:          []string{"web/src/clients", "sdk/generated"},
	}
	module.WithDir(dir)
	if err := module.Save(ctx); err != nil {
		t.Fatalf("save module: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, ModuleConfigurationName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "vendored:\n") {
		t.Fatalf("the manifest does not carry the declaration under its one key:\n%s", written)
	}
	loaded, err := LoadModuleFromDir(ctx, dir)
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	if got := strings.Join(loaded.Vendored, ","); got != "web/src/clients,sdk/generated" {
		t.Fatalf("vendored = %q, want the declaration as written", got)
	}
	// Cleared, the key goes: the absence of vendored code is the common case
	// and the manifest does not grow an empty list for it.
	loaded.Vendored = nil
	if err := loaded.Save(ctx); err != nil {
		t.Fatalf("save module: %v", err)
	}
	written, err = os.ReadFile(filepath.Join(dir, ModuleConfigurationName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "vendored") {
		t.Fatalf("a cleared declaration was still written:\n%s", written)
	}
}

// The manifest's decoder is STRICT: a key the type does not declare is refused
// at load, naming it, never dropped. This is what makes the declaration a fact
// a signed document can repeat — a lenient loader read `vendorred:` as no
// declaration at all, the counter then included the kit, and the document
// truthfully reported that nothing was excluded.
func TestAModuleManifestRefusesAKeyItDoesNotDeclare(t *testing.T) {
	ctx := context.Background()
	for name, content := range map[string]string{
		"misspelt vendored": "kind: module\nname: saas\nservices:\n  - name: api\nvendorred:\n  - web/src/clients\n",
		"unknown key":       "kind: module\nname: saas\nservices:\n  - name: api\nowner: platform\n",
		"capitalised key":   "kind: module\nname: saas\nservices:\n  - name: api\nVendored:\n  - web/src/clients\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadModuleFromDir(ctx, dir)
			if err == nil {
				t.Fatal("the manifest loaded with a key no module declares")
			}
			if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no module manifest declares") {
				t.Fatalf("the refusal does not name the unknown key: %v", err)
			}
		})
	}
}

// The manifest's tree is held to the node-level checks the wire documents
// share before the typed decoder reads a field: a null vendored list would
// otherwise read as "no declaration", and a merge key would carry a second
// vendored list in after every check had run and shadow the written one.
//
// Every row names the RULE it trips and asserts ErrInvalidManifestWireForm, and
// wire.Kinds() holds the table to one row per rule. The sentinel went onto all
// five refusals with one row asserting it: the wrap could be deleted from the
// duplicate-key, merge-key, fraction or key-not-a-name refusal with the whole
// suite still green, and two of those rules had no row here at all.
func TestAModuleManifestIsHeldToTheSharedNodeChecks(t *testing.T) {
	ctx := context.Background()
	// kitClause says whether the refusal must name the vendored consequence.
	// It is stated per row and never derived from the message, which is how
	// the old expectation agreed with the prefix match it should have caught.
	// A message ending in a space is not a typo: the null defect carries no
	// detail, and the space is what tells `vendored ` from `vendored-cache`.
	table := map[string]struct {
		content, message string
		rule             wire.DefectKind
		kitClause        bool
	}{
		"null vendored list":    {"kind: module\nname: saas\nservices:\n  - name: api\nvendored: ~\n", "explicit null at vendored ", wire.NullNode, true},
		"null vendored element": {"kind: module\nname: saas\nservices:\n  - name: api\nvendored:\n  - ~\n", "explicit null at vendored[0]", wire.NullNode, true},
		"null services list":    {"kind: module\nname: saas\nservices: ~\n", "explicit null at services", wire.NullNode, false},
		// A key is not the vendored field because it begins with its letters.
		"null under a key that only begins with vendored": {"kind: module\nname: saas\nservices:\n  - name: api\nvendored-cache: ~\n", "explicit null at vendored-cache", wire.NullNode, false},
		"merge key":                   {"kind: module\nname: saas\nservices:\n  - name: api\nvendored: [a]\n<<:\n  vendored: [b]\n", "uses the merge key", wire.MergeKey, false},
		"fraction":                    {"kind: module\nname: saas\nservices:\n  - name: api\nweight: 1.5\n", "not a whole number", wire.FractionalNumber, false},
		"duplicate key":               {"kind: module\nname: saas\nname: other\nservices:\n  - name: api\n", `names the key "name" twice`, wire.DuplicateKey, false},
		"a sequence written as a key": {"kind: module\nname: saas\nservices:\n  - name: api\n? [a]\n: b\n", `a key that is not a name ("!!seq at line 5")`, wire.KeyNotAName, false},
	}
	for name, manifest := range table {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(manifest.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadModuleFromDir(ctx, dir)
			if err == nil || !strings.Contains(err.Error(), manifest.message) {
				t.Fatalf("the manifest must be refused naming %q: %v", manifest.message, err)
			}
			// The authority that refuses names itself. Without this the five
			// refusals could carry any sentinel, or none, and the one caller
			// that asks "was the wire form refused" would read false.
			if !errors.Is(err, ErrInvalidManifestWireForm) {
				t.Fatalf("a manifest refused on its wire form says so by its own sentinel: %v", err)
			}
			// The vendored consequence names the vendored FIELD's stake: a
			// null there would count the kit while the manifest said it was
			// excluded. No other key has that stake.
			if got := strings.Contains(err.Error(), "would count the kit"); got != manifest.kitClause {
				t.Fatalf("the vendored consequence must appear exactly for the vendored field (wanted %v): %v", manifest.kitClause, err)
			}
		})
	}
	// One row per rule. A rule with none is a rule nothing would notice
	// losing its refusal or its sentinel — which is how the interface table's
	// null row came to assert a sentinel that never fired for it.
	covered := map[wire.DefectKind]bool{}
	for _, manifest := range table {
		covered[manifest.rule] = true
	}
	for _, rule := range wire.Kinds() {
		if !covered[rule] {
			t.Errorf("wire rule %d refuses a module manifest with no row in this table", rule)
		}
	}
}

// Two keys on disk predate every model of the manifest: `project` and
// `domain`, written by an earlier workspace layout and read by nothing. The
// lenient loader dropped them silently; the strict one refuses them like any
// unknown key, but names them with the remedy — delete them — so a workspace
// that loaded yesterday reads why it does not today in one line.
func TestVestigialManifestKeysAreRefusedWithTheirRemedy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	content := "kind: module\nname: saas\nproject: platform\ndomain: example.com\nservices:\n  - name: api\n"
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadModuleFromDir(ctx, dir)
	if err == nil {
		t.Fatal("a manifest with vestigial keys loaded")
	}
	for _, want := range []string{"project (line 3)", "domain (line 4)", "no module manifest declares", "delete them"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// With the keys deleted, the same manifest loads.
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte("kind: module\nname: saas\nservices:\n  - name: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModuleFromDir(ctx, dir); err != nil {
		t.Fatalf("the remedied manifest must load: %v", err)
	}
}

// A composition descriptor shares the module manifest's file name and is told
// apart by its kind. Loaded as a module it is refused by name and pointed at
// its own reader, never loaded as a module with nothing in it; the fleet's
// descriptors carry base, contributions and bindings, which this decoder would
// also refuse as unknown keys, but the kind is what names the mistake.
func TestACompositionDescriptorIsNotAModuleManifest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	descriptor := `kind: composed-module
name: example-product
base:
  id: example/base
  version: ">=0.1.0 <0.2.0"
contributions:
  frontend:
    - path: frontend
      export: exampleFrontendPlugin
bindings:
  - plugin: example-product
    alias: api
    target:
      module: example
      service: api
`
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(descriptor), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadModuleFromDir(ctx, dir)
	if err == nil {
		t.Fatal("a composition descriptor loaded as a module")
	}
	// Named BY ITS KIND, from the node, before the typed decoder could refuse
	// it for its first unknown key (base) and never say what it is.
	if !strings.Contains(err.Error(), "composition.LoadDescriptor") {
		t.Fatalf("a real descriptor must be refused by name, not for its first unknown key: %v", err)
	}

	// With only the kind wrong, the kind is what is named.
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte("kind: composed-module\nname: example-product\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadModuleFromDir(ctx, dir)
	if err == nil || !strings.Contains(err.Error(), "composition.LoadDescriptor") {
		t.Fatalf("the descriptor kind must be refused by name: %v", err)
	}
	// And any other kind is refused as not a module manifest.
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte("kind: service\nname: example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadModuleFromDir(ctx, dir)
	if err == nil || !strings.Contains(err.Error(), `kind "service"`) {
		t.Fatalf("a foreign kind must be refused by name: %v", err)
	}
	// An absent kind is a module manifest: the file's name and place say
	// what it is, and the kind is dispatch for the one document that shares
	// the name.
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte("name: example\nservices:\n  - name: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModuleFromDir(ctx, dir); err != nil {
		t.Fatalf("a manifest without a kind is a module manifest: %v", err)
	}
}

// The declaration is refused at load for the same three reasons the presence
// document refuses the excluded list: a spelling that is not canonical, an
// entry twice, and an entry under another. The load chain is the one the CLI
// uses (LoadModuleFromDir -> postLoad -> validatePaths).
func TestModuleVendoredPathsAreHeldToOneSpelling(t *testing.T) {
	ctx := context.Background()
	for name, declared := range map[string]struct {
		vendored []string
		message  string
	}{
		"trailing slash":       {[]string{"web/src/clients/"}, "not a canonical relative path"},
		"leading dot-slash":    {[]string{"./web/src/clients"}, "not a canonical relative path"},
		"absolute":             {[]string{"/web/src/clients"}, "not a canonical relative path"},
		"escaping":             {[]string{"../other/clients"}, "not a canonical relative path"},
		"empty entry":          {[]string{""}, "not a canonical relative path"},
		"twice":                {[]string{"web/src/clients", "web/src/clients"}, "twice"},
		"nested":               {[]string{"web/src/clients", "web/src/clients/go"}, "overlap"},
		"nested, outer second": {[]string{"web/src/clients/go", "web/src/clients"}, "overlap"},
		"not utf-8":            {[]string{"vendor/\xff"}, "not a canonical relative path"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			content := "kind: module\nname: saas\nservices:\n  - name: api\nvendored:\n"
			for _, entry := range declared.vendored {
				content += "  - " + yamlScalar(entry) + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadModuleFromDir(ctx, dir)
			if err == nil {
				t.Fatalf("the module loaded with vendored %q", declared.vendored)
			}
			if !strings.Contains(err.Error(), declared.message) {
				t.Fatalf("the refusal does not name the defect %q: %v", declared.message, err)
			}
		})
	}
}

// yamlScalar writes an entry so the test can state the exact bytes under test:
// quoted for an empty string or a path starting with a dot, and as !!binary
// for bytes that are not UTF-8, which a quoted scalar cannot carry.
func yamlScalar(value string) string {
	if !utf8.ValidString(value) {
		return "!!binary " + base64.StdEncoding.EncodeToString([]byte(value))
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

// TestAnAliasBombIsAnsweredAtOnce: the manifest loader runs the shared node
// check before the typed decoder, so it inherited a walk that followed every
// alias — a 579-byte manifest of fan-ten aliases took 132 seconds at nine
// levels where the lenient loader answered at once. An alias target is walked
// once now; twelve levels are refused (the bomb's keys are unknown to a module
// manifest) in well under a second.
func TestAnAliasBombIsAnsweredAtOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("kind: module\nname: saas\nservices:\n  - name: api\n")
	b.WriteString("x0: &a0 [lol]\n")
	for n := 1; n <= 12; n++ {
		b.WriteString(fmt.Sprintf("x%d: &a%d [", n, n))
		for i := 0; i < 10; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("*a%d", n-1))
		}
		b.WriteString("]\n")
	}
	start := time.Now()
	if _, err := LoadFromBytes[Module]([]byte(b.String())); err == nil {
		t.Fatal("the bomb's keys are unknown to a module manifest and must be refused")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the loader took %s; an alias target is walked once", elapsed)
	}
}

// The kind is dispatched on its RESOLVED value: a kind written as an alias to
// a string anchored elsewhere is the string, as the typed decoder would read
// it. A reader that matched the written text let `kind: *k` load a composition
// descriptor as a module of kind composed-module. Witnessed through both
// loaders, and a kind that resolves to anything but a plain string is refused.
func TestTheKindIsDispatchedOnItsResolvedValue(t *testing.T) {
	ctx := context.Background()
	aliased := "description: &k composed-module\nkind: *k\nname: example\n"
	_, err := LoadFromBytes[Module]([]byte(aliased))
	if err == nil || !strings.Contains(err.Error(), "composition.LoadDescriptor") {
		t.Fatalf("LoadFromBytes must refuse the aliased descriptor kind by name: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(aliased), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadModuleFromDir(ctx, dir)
	if err == nil || !strings.Contains(err.Error(), "composition.LoadDescriptor") {
		t.Fatalf("LoadModuleFromDir must refuse the aliased descriptor kind by name: %v", err)
	}
	// An alias to the module kind loads, as it names the module kind.
	loaded, err := LoadFromBytes[Module]([]byte("description: &k module\nkind: *k\nname: example\nservices:\n  - name: api\n"))
	if err != nil || loaded.Kind != ModuleKind {
		t.Fatalf("an aliased module kind is the module kind: %v", err)
	}
	for name, content := range map[string]string{
		"a mapping":  "kind: {a: b}\nname: example\n",
		"an integer": "kind: 1\nname: example\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromBytes[Module]([]byte(content))
			if err == nil || !strings.Contains(err.Error(), "kind that is not a plain string") {
				t.Fatalf("a kind that is not a plain string must be refused as such: %v", err)
			}
		})
	}
}
