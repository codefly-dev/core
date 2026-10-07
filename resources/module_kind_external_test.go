package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corecomposition "github.com/codefly-dev/core/composition"
	"github.com/codefly-dev/core/resources"
)

// TestTheDescriptorKindIsCompositions holds the module loader's spelling of the
// composition descriptor's kind to composition's own: the loader refuses that
// kind by name and cannot import the package that owns it (composition imports
// resources), so the two spellings are held together here, from outside both.
func TestTheDescriptorKindIsCompositions(t *testing.T) {
	dir := t.TempDir()
	content := "kind: " + corecomposition.DescriptorKind + "\nname: example\n"
	if err := os.WriteFile(filepath.Join(dir, resources.ModuleConfigurationName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resources.LoadModuleFromDir(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "composition.LoadDescriptor") {
		t.Fatalf("the module loader does not recognise composition's descriptor kind %q: %v", corecomposition.DescriptorKind, err)
	}
}
