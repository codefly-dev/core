package configurations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
)

// The format a configuration file's content is in, which is its extension: it
// is what ConfigurationData.Kind carries for a structured group, and what the
// local reader reads a file as. They are named because the same four strings
// decide parsing in three files of this package, and a typo in one of them
// reads as "an unsupported kind" at the far end of a load.
const (
	kindEnv  = "env"
	kindYaml = "yaml"
	kindYml  = "yml"
	kindJSON = "json"
)

func ConfigurationInformationDataFromFile(ctx context.Context, name string, p string, isSecret bool) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.ConfigurationInformationDataFromFile")
	content, err := os.ReadFile(p) //nolint:gosec // p is a configuration file of the workspace being read
	if err != nil {
		return nil, w.Wrapf(err, "cannot read yaml env file")
	}
	extension := filepath.Ext(p)
	info := &basev0.ConfigurationInformation{
		Name: name,
		Data: &basev0.ConfigurationData{
			Kind:    extension[1:],
			Content: content,
			Secret:  isSecret,
		},
	}
	return info, nil
}

func InformationUnmarshal(info *basev0.ConfigurationInformation, v interface{}) error {
	if info.Data == nil {
		return nil
	}
	if info.Data.Kind == kindYaml {
		return yaml.Unmarshal(info.Data.Content, v)
	}
	if info.Data.Kind == kindJSON {
		return json.Unmarshal(info.Data.Content, v)
	}
	return fmt.Errorf("unsupported kind %s", info.Data.Kind)
}
