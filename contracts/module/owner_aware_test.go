package module

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnerAwareIsAnExplicitDeclaration(t *testing.T) {
	for _, declaration := range []string{"", "owner_aware: false\n", "owner_aware: true\n"} {
		t.Run(strings.TrimSpace(declaration), func(t *testing.T) {
			data := append(fixture(t), []byte(declaration)...)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
				t.Fatal(err)
			}
			contract, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Contains(declaration, "true")
			if contract.OwnerAware != want {
				t.Fatalf("owner-aware = %v, want %v", contract.OwnerAware, want)
			}
			resolved, err := contract.Resolve(values())
			if err != nil {
				t.Fatal(err)
			}
			if resolved.OwnerAware != want {
				t.Fatalf("resolution changed owner-aware to %v", resolved.OwnerAware)
			}
			encoded, err := contract.Encode()
			if err != nil {
				t.Fatal(err)
			}
			again, err := Parse(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if again.OwnerAware != want {
				t.Fatal("encoding changed the declaration")
			}
		})
	}
	// Neither roles nor owned scopes assert that the solution checks owners.
	data, err := os.ReadFile(filepath.Join("testdata", "roles-installation-owned.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if contract.OwnerAware {
		t.Fatal("roles implied owner-aware checking")
	}
}

func TestOwnerAwareDecodingIsStrict(t *testing.T) {
	for _, field := range []string{
		"owner_aware: yes", "owner_aware: no", `owner_aware: "true"`,
		"owner_aware: 1", "owner_aware: []", "owner_aware: {}", "owner_aware: null",
		"owner_aware: true\nowner_aware: false", "owner-aware: true",
		"owner_aware: &value yes\nrequire_method_policies: *value",
		"require_method_policies: &value yes\nowner_aware: *value",
	} {
		t.Run(field, func(t *testing.T) {
			_, err := Parse(append(fixture(t), []byte(field+"\n")...))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid declaration, got %v", err)
			}
			if !strings.Contains(err.Error(), "owner_aware") && !strings.Contains(err.Error(), "owner-aware") {
				t.Fatalf("refusal does not name the declaration: %v", err)
			}
		})
	}
}
