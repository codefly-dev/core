package agents

import (
	"testing"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
)

func TestToolboxRegistration_LocalCompatibilityIsExplicitlyUnguarded(t *testing.T) {
	t.Setenv(policy.EnvPermissionsSocket, "")
	t.Setenv("CODEFLY_SCOPED_AUTHZ_SECRET", "")
	reg := toolboxRegistration(&toolboxv0.UnimplementedToolboxServer{})
	if reg.PDP != nil {
		t.Fatal("local compatibility registration unexpectedly installed a PDP")
	}
}

func TestToolboxRegistration_ProductionEnvInstallsCallbackGuard(t *testing.T) {
	t.Setenv(policy.EnvPermissionsSocket, "/tmp/codefly-test-permissions.sock")
	t.Setenv("CODEFLY_SCOPED_AUTHZ_SECRET", "")
	t.Setenv("CODEFLY_TOOLBOX_AUDIENCE", "codefly.dev/fixture:0.0.1")
	reg := toolboxRegistration(&toolboxv0.UnimplementedToolboxServer{})
	if reg.PDP == nil {
		t.Fatal("permissions callback must install a PDP")
	}
	if reg.PDPToolboxName != "codefly.dev/fixture:0.0.1" {
		t.Fatalf("unexpected audience %q", reg.PDPToolboxName)
	}
}

func TestToolboxRegistration_ScopedAuthWithoutCallbackStillFailsClosed(t *testing.T) {
	t.Setenv(policy.EnvPermissionsSocket, "")
	t.Setenv("CODEFLY_SCOPED_AUTHZ_SECRET", "01234567890123456789012345678901")
	t.Setenv("CODEFLY_TOOLBOX_AUDIENCE", "")
	t.Setenv("CODEFLY_TOOLBOX_NAME", "fixture")
	reg := toolboxRegistration(&toolboxv0.UnimplementedToolboxServer{})
	if reg.PDP == nil {
		t.Fatal("scoped authorization must install a defense-path PDP")
	}
	if reg.PDPToolboxName != "fixture" {
		t.Fatalf("expected name fallback audience, got %q", reg.PDPToolboxName)
	}
}
