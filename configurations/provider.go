package configurations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

const ProjectProviderOrigin = "ProjectProviderOrigin"

func loadFromEnvFile(ctx context.Context, dir string, p string) (*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromEnvFile")

	// Extract the relative path
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get relative path")
	}
	rel = filepath.Dir(rel)
	origin := ProjectProviderOrigin
	if rel != "." {
		parsed, err := ParseServiceUnique(rel)
		if err != nil {
			return nil, w.Wrapf(err, "cannot parse service unique")
		}
		origin = parsed.Unique()
	}
	base := filepath.Base(p)
	var ok bool
	base, ok = strings.CutSuffix(base, ".env")
	if !ok {
		return nil, w.NewError("invalid env file name: %s", base)
	}

	f, err := os.ReadFile(p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read auth0.env")
	}
	info := &basev0.ProviderInformation{
		Name:   base,
		Origin: origin,
		Data:   make(map[string]string),
	}
	lines := strings.Split(string(f), "\n")

	for _, line := range lines {
		tokens := strings.Split(line, "=")
		if len(tokens) != 2 {
			continue
		}
		info.Data[tokens[0]] = tokens[1]
	}
	return info, nil
}

func LoadProviderFromEnvFiles(ctx context.Context, project *Project, env *Environment) ([]*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromProject")
	var infos []*basev0.ProviderInformation
	dir := filepath.Join(project.Dir(), "providers", env.Name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, w.NewError("path doesn't exist: %s", dir)
	}
	// Walk
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if info.Name() == ".env" {
			return nil
		}
		prov, nerr := loadFromEnvFile(ctx, dir, path)
		if nerr != nil {
			return w.Wrapf(nerr, "cannot load provider from env file")
		}
		infos = append(infos, prov)
		return nil
	})

	if err != nil {
		return nil, w.Wrapf(err, "cannot walk providers directory")
	}
	return infos, nil
}

const ProviderPrefix = "CODEFLY_PROVIDER__"

func ProviderInformationAsEnvironmentVariables(info *basev0.ProviderInformation) []string {
	var env []string
	for key, value := range info.Data {
		env = append(env, ProviderInformationEnv(info, key, value))
	}
	return env
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
