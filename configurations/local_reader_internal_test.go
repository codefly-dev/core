package configurations

import (
	"errors"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// Two service origins that normalize to the same environment key make the
// override-target index ambiguous. When no overrides are supplied the index is
// never consulted, so building (and ambiguity-checking) it would let an unused
// feature fail an otherwise valid configuration load.
func TestApplyServiceConfigurationOverridesIgnoresOriginCollisionWhenUnused(t *testing.T) {
	confs := map[string]*basev0.Configuration{}
	origins := []string{"team/a-b", "team/a_b"} // both normalize to TEAM__A_B

	got, err := applyServiceConfigurationOverrides(confs, origins, "")
	if err != nil {
		t.Fatalf("unused overrides must not fail on an origin key collision: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("configurations must be returned unchanged, got %d", len(got))
	}
}

// The ambiguity guard must remain in force when an override is actually
// supplied: option order can never be allowed to pick a credential target.
func TestApplyServiceConfigurationOverridesRejectsOriginCollisionWhenTargeted(t *testing.T) {
	encoded, err := resources.EncodeServiceConfigurationOverrides([]resources.ServiceConfigurationOverride{
		{Service: "team/a-b", Name: "postgres", Key: "POSTGRES_USER", Value: "x"},
	})
	if err != nil {
		t.Fatalf("encode override: %v", err)
	}
	origins := []string{"team/a-b", "team/a_b"} // both normalize to TEAM__A_B

	_, err = applyServiceConfigurationOverrides(map[string]*basev0.Configuration{}, origins, encoded)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("a targeted override with colliding origins must be rejected, got %v", err)
	}
}

// The overlay boundary refuses a duplicate canonical key on either side. With
// the per-layer refusal in profileOverlay.add, no profile-loaded input can
// reach here carrying one, so this is the boundary stating its own precondition
// rather than a reachable path — and it is tested directly, because a guard
// whose only justification is "nothing can get here" is the guard that silently
// stops holding when a second caller appears.
func TestOverlayWorkspaceConfigurationOverrideRefusesADuplicateKeyOnEitherSide(t *testing.T) {
	group := func(values ...*basev0.ConfigurationValue) *basev0.ConfigurationInformation {
		return &basev0.ConfigurationInformation{Name: "app-config", ConfigurationValues: values}
	}
	value := func(key, v string) *basev0.ConfigurationValue {
		return &basev0.ConfigurationValue{Key: key, Value: v}
	}

	for _, test := range []struct {
		name     string
		base     *basev0.ConfigurationInformation
		override *basev0.ConfigurationInformation
		expect   string
	}{
		{
			name:     "the module's group declares one key twice",
			base:     group(value("CONFIG_FILE", ProfileValueMarker), value("config-file", ProfileValueMarker)),
			override: group(value("CONFIG_FILE", "/etc/app/solution.yaml")),
			expect:   "composed module",
		},
		{
			name:     "the override declares one key twice",
			base:     group(value("CONFIG_FILE", ProfileValueMarker)),
			override: group(value("CONFIG_FILE", "/etc/app/solution.yaml"), value("config_file", "")),
			expect:   "the consuming workspace declares",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := overlayWorkspaceConfigurationOverride(test.base, test.override, "host")
			if err == nil {
				t.Fatalf("a duplicate canonical key must be refused, not resolved by position")
			}
			if !errors.Is(err, ErrConfigurationConflict) {
				t.Fatalf("want ErrConfigurationConflict, got %v", err)
			}
			if !strings.Contains(err.Error(), test.expect) {
				t.Fatalf("the diagnostic must name which side declared it, got %v", err)
			}
		})
	}
}
