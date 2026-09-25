package proto

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The bindings and the facade are loaded as ES modules. Node's ESM loader
// resolves a relative import only with its extension, so both plugins must
// emit `.js` on relative imports; an extensionless tree works only inside a
// bundler and fails every Node consumer on its first import.
func TestTypeScriptClientImportsCarryTheJsExtension(t *testing.T) {
	dir := t.TempDir()
	facade := FacadeOptions{Facade: true, Services: []string{"AuditService"}, Module: "accounts"}
	if err := templateTypeScriptConfiguration(context.Background(), dir, facade, false); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	opts := regexp.MustCompile(`(?m)^\s*opt:\s*(.+)$`).FindAllSubmatch(rendered, -1)
	if len(opts) != 2 {
		t.Fatalf("want the bindings' and the facade's options, got %d:\n%s", len(opts), rendered)
	}
	for _, opt := range opts {
		if !regexp.MustCompile(`(^|,)import_extension=js(,|$)`).Match(opt[1]) {
			t.Errorf("plugin options %q do not emit .js on relative imports", opt[1])
		}
	}
	if !regexp.MustCompile(`services=AuditService,module=accounts`).Match(rendered) {
		t.Errorf("the facade lost its own options:\n%s", rendered)
	}
}
