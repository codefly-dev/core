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

func ConfigurationInformationDataFromFile(ctx context.Context, name string, p string, isSecret bool) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.ConfigurationInformationDataFromFile")
	content, err := os.ReadFile(p)
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
	if info.Data.Kind == "yaml" {
		return yaml.Unmarshal(info.Data.Content, v)
	}
	if info.Data.Kind == "json" {
		return json.Unmarshal(info.Data.Content, v)
	}
	return fmt.Errorf("unsupported kind %s", info.Data.Kind)
}
