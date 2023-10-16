package configurations_test

import (
	"github.com/hygge-io/hygge/pkg/configurations"
	"github.com/hygge-io/hygge/pkg/core"
	"testing"
)

func TestPluginParse(t *testing.T) {
	tcs := []struct {
		name string
		in   string
		out  *configurations.Plugin
	}{
		{name: "identifier only", in: "go-grpc", out: &configurations.Plugin{Kind: configurations.PluginService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "latest"}},
		{name: "identifier with publisher", in: "go-grpc:0.0.0", out: &configurations.Plugin{Kind: configurations.PluginService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "0.0.0"}},
		{name: "full specification", in: "codefly.ai/go-grpc:0.0.0", out: &configurations.Plugin{Kind: configurations.PluginService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "0.0.0"}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			p, err := configurations.ParsePlugin(configurations.PluginService, tc.in)
			if err != nil && !core.IsUserWarning(err) {
				t.Fatal(err)
			}
			if p.Kind != tc.out.Kind {
				t.Fatalf("expected kind %s, got %s", tc.out.Kind, p.Kind)
			}
			if p.Publisher != tc.out.Publisher {
				t.Fatalf("expected publisher %s, got %s", tc.out.Publisher, p.Publisher)
			}
			if p.Identifier != tc.out.Identifier {
				t.Fatalf("expected identifier %s, got %s", tc.out.Identifier, p.Identifier)
			}
			if p.Version != tc.out.Version {
				t.Fatalf("expected version %s, got %s", tc.out.Version, p.Version)
			}
		})
	}
}
