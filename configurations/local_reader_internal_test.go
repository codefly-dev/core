package configurations

import (
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
