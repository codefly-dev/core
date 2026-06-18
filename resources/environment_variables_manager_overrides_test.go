package resources

import "testing"

func TestAddOverridesReachesAll(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.AddOverrides(map[string]string{
		"CODEFLY__FIXTURE": "dogfood",
		"FOO":              "bar",
	})

	envs, err := holder.All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}

	got := map[string]string{}
	for _, e := range envs {
		got[e.Key] = e.ValueAsString()
	}
	if got["CODEFLY__FIXTURE"] != "dogfood" {
		t.Errorf("CODEFLY__FIXTURE = %q, want dogfood", got["CODEFLY__FIXTURE"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", got["FOO"])
	}
}
