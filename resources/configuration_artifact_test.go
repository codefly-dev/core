package resources

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func artifactConfiguration(value string) *basev0.Configuration {
	sum := sha256.Sum256([]byte(value))
	return &basev0.Configuration{Origin: ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{Name: "test-group", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "MY_KEY", Value: value, ResolvedModuleArtifact: &basev0.ResolvedConfigurationModuleArtifact{Reference: &basev0.ConfigurationModuleArtifactReference{Module: "documents", Path: "public/install.json"}, ArtifactIdentity: &basev0.SourceIdentity{Algorithm: basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, Digest: fmt.Sprintf("%x", sum), SizeBytes: int64(len(value))}}}}}}}
}

func TestModuleArtifactDeliveryProtectsEveryOccurrence(t *testing.T) {
	baseline := artifactConfiguration("A")
	require.NoError(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline}, nil, nil))
	for _, text := range []string{"A", "B"} {
		for _, annotated := range []bool{false, true} {
			additional := artifactConfiguration(text)
			additional.Infos[0].Name = "TEST_GROUP"
			additional.Infos[0].ConfigurationValues[0].Key = "my-key"
			if !annotated {
				additional.Infos[0].ConfigurationValues[0].ResolvedModuleArtifact = nil
			}
			require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline}, []*basev0.Configuration{additional}, nil))
			require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline, additional}, nil, nil))
		}
	}
	require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline, baseline}, nil, nil))
	other := proto.Clone(baseline).(*basev0.Configuration)
	other.Origin = "m/service"
	require.NoError(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline}, []*basev0.Configuration{other}, nil))
	ordinary := proto.Clone(baseline).(*basev0.Configuration)
	ordinary.Infos[0].ConfigurationValues[0].ResolvedModuleArtifact = nil
	require.NoError(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{ordinary, ordinary}, nil, nil))
	document := &basev0.Configuration{Origin: ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{Name: "test_group", Data: &basev0.ConfigurationData{Content: []byte("{}")}}}}
	require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline}, []*basev0.Configuration{document}, nil))
}

func TestModuleArtifactCarrierAliases(t *testing.T) {
	for _, origin := range []string{ConfigurationWorkspace, "m/service"} {
		conf := artifactConfiguration("A")
		conf.Origin = origin
		public := ConfigurationValueEnvironmentKey(conf, "test-group", conf.Infos[0].ConfigurationValues[0])
		for _, secret := range []bool{false, true} {
			for _, legacy := range []bool{false, true} {
				for _, file := range []bool{false, true} {
					key := public
					if secret {
						key = strings.ReplaceAll(strings.ReplaceAll(key, WorkspaceConfigurationPrefix, WorkspaceSecretConfigurationPrefix), ServiceConfigurationPrefix, ServiceSecretConfigurationPrefix)
					}
					if legacy {
						key = strings.ReplaceAll(key, "TEST_GROUP", "TEST-GROUP")
					}
					if file {
						key = FileCarrierKey(key)
					}
					require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{conf}, nil, map[string]string{key: "A"}), key)
				}
			}
		}
		require.NoError(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{conf}, nil, map[string]string{"CODEFLY__OTHER": "A"}))
	}
}

func TestModuleArtifactEmittersValidateBeforeFiltering(t *testing.T) {
	for _, mutate := range []func(*basev0.ConfigurationValue){
		func(v *basev0.ConfigurationValue) { v.Value = "B" },
		func(v *basev0.ConfigurationValue) { v.Secret = true },
		func(v *basev0.ConfigurationValue) { v.Template = &basev0.ConfigurationValueTemplate{} },
		func(v *basev0.ConfigurationValue) { v.ModuleArtifact = v.ResolvedModuleArtifact.Reference },
		func(v *basev0.ConfigurationValue) { v.ResolvedModuleArtifact.ArtifactIdentity = nil },
		func(v *basev0.ConfigurationValue) { v.ResolvedModuleArtifact.ArtifactIdentity.SizeBytes++ },
		func(v *basev0.ConfigurationValue) {
			v.ResolvedModuleArtifact.ArtifactIdentity.Digest = strings.Repeat("A", 64)
		},
		func(v *basev0.ConfigurationValue) { v.ResolvedModuleArtifact.Reference.Path = "../escape" },
	} {
		conf := artifactConfiguration("A")
		mutate(conf.Infos[0].ConfigurationValues[0])
		for _, secret := range []bool{false, true} {
			_, err := ConfigurationAsEnvironmentVariables(conf, "local", secret)
			require.Error(t, err)
		}
		_, err := ConfigurationAsRawEnvironmentVariables(conf)
		require.Error(t, err)
	}
	for _, n := range []int{9406, FileCarrierThreshold} {
		value := "{\n" + strings.Repeat(" ", n-3) + "}"
		conf := artifactConfiguration(value)
		cloned := proto.Clone(conf).(*basev0.Configuration)
		env, err := ConfigurationAsEnvironmentVariables(cloned, "local", false)
		require.NoError(t, err)
		require.False(t, env[0].File)
		require.Equal(t, value, env[0].Value)
	}
	for _, value := range []string{strings.Repeat("x", FileCarrierThreshold+1), "\xff", "nul\x00", "${profile}", "${endpoint:m/s/http}", "${endpoint:bad"} {
		require.Error(t, ValidateModuleArtifactValue(artifactConfiguration(value).Infos[0].ConfigurationValues[0]))
	}
}

func TestModuleArtifactExplicitRawCarrierContribution(t *testing.T) {
	baseline := artifactConfiguration("A")
	key := ConfigurationValueEnvironmentKey(baseline, "test-group", baseline.Infos[0].ConfigurationValues[0])
	raw := &basev0.Configuration{Origin: "other/service", Infos: []*basev0.ConfigurationInformation{{Name: "explicit", ConfigurationValues: []*basev0.ConfigurationValue{{Key: key, Value: "B"}}}}}
	require.Error(t, ValidateModuleArtifactDelivery([]*basev0.Configuration{baseline}, []*basev0.Configuration{raw}, nil))
}
