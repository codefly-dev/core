package configurations

import (
	"context"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"
)

// A templated value holds no value to resolve. Running the reference parser over
// its empty string reported "not a reference" and warned about a plaintext
// secret that does not exist — and because that warning fires once per origin,
// it then swallowed the warning the genuine plaintext secret beside it should
// have produced.
func TestSecretResolutionIgnoresATemplatedValue(t *testing.T) {
	capture := &secretLogCapture{}
	ctx := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "templated"}).
		WithLogger(capture).Inject(context.Background())
	previousLevel := wool.GlobalLogLevel()
	wool.SetGlobalLogLevel(wool.TRACE)
	t.Cleanup(func() { wool.SetGlobalLogLevel(previousLevel) })

	resolution := &secretResolution{
		resolvers: map[string]SecretResolver{OnePasswordScheme: &recordingSecretResolver{}},
		cache:     map[string]string{},
		warned:    map[string]bool{},
	}
	conf := &basev0.Configuration{
		Origin: "mod/store",
		Infos: []*basev0.ConfigurationInformation{{
			Name: "postgres",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "CONNECTION", Secret: true, Template: &basev0.ConfigurationValueTemplate{
					Segments: []*basev0.ConfigurationValueTemplateSegment{
						{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "postgresql://x"}},
					},
				}},
			},
		}},
	}
	require.NoError(t, resolution.resolveConfiguration(ctx, conf, &resources.Environment{Name: "production"}))
	for _, log := range capture.logs {
		require.NotContains(t, log.String(), "plaintext secret",
			"a templated value is not a plaintext secret")
	}
	require.False(t, resolution.warned["mod/store"],
		"a templated value must not spend the origin's one plaintext warning")

	// The warning still fires for a value that really is plaintext, which the
	// suppressed flag would have hidden.
	plain := &basev0.Configuration{
		Origin: "mod/store",
		Infos: []*basev0.ConfigurationInformation{{
			Name:                "postgres",
			ConfigurationValues: []*basev0.ConfigurationValue{{Key: "POSTGRES_PASSWORD", Value: "hunter2", Secret: true}},
		}},
	}
	require.NoError(t, resolution.resolveConfiguration(ctx, plain, &resources.Environment{Name: "production"}))
	var warned bool
	for _, log := range capture.logs {
		if strings.Contains(log.String(), "plaintext secret") {
			warned = true
		}
	}
	require.True(t, warned, "a genuine plaintext secret must still be reported")
}
