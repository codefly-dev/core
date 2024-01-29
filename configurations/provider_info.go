package configurations

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

const ProjectProviderOrigin = "ProjectProviderOrigin"

const ProviderPrefix = "CODEFLY_PROVIDER__"

func ProviderInformationAsEnvironmentVariables(info *basev0.ProviderInformation) []string {
	var env []string
	for key, value := range info.Data {
		env = append(env, ProviderInformationEnv(info, key, value))
	}
	return env
}

func FindProjectProvider(name string, sources []*basev0.ProviderInformation) (*basev0.ProviderInformation, error) {
	for _, prov := range sources {
		if prov.Origin != ProjectProviderOrigin {
			continue
		}
		if prov.Name == name {
			return prov, nil
		}
	}
	return nil, fmt.Errorf("cannot find provider: %s", name)
}

func ProviderInformationEnv(info *basev0.ProviderInformation, key string, value string) string {
	key = ProviderInformationEnvKey(info, key)
	return fmt.Sprintf("%s=%s", key, value)
}

func ProviderInformationEnvKey(info *basev0.ProviderInformation, key string) string {
	if info.Origin == ProjectProviderOrigin {
		return fmt.Sprintf("%s_%s____%s", ProviderPrefix, strings.ToUpper(info.Name), strings.ToUpper(key))
	}
	origin := sanitizeUnique(info.Origin)
	return fmt.Sprintf("%s%s___%s____%s", ProviderPrefix, strings.ToUpper(origin), strings.ToUpper(info.Name), strings.ToUpper(key))
}

func sanitizeUnique(origin string) string {
	return strings.Replace(origin, "/", "__", 1)
}

func providerInformationHash(info *basev0.ProviderInformation) string {
	return HashString(info.String())
}

func ProviderInformationHash(infos ...*basev0.ProviderInformation) (string, error) {
	hasher := NewHasher()
	for _, info := range infos {
		hasher.Add(providerInformationHash(info))
	}
	return hasher.Hash(), nil
}

func ProviderInfoSummary(info *basev0.ProviderInformation) string {
	return fmt.Sprintf("%s/%s", info.Origin, info.Name)
}

func MakeProviderInfosSummary(infos []*basev0.ProviderInformation) string {
	var summary []string
	for _, info := range infos {
		summary = append(summary, ProviderInfoSummary(info))
	}
	return strings.Join(summary, ", ")
}
