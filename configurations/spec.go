package configurations

import (
	"github.com/hygge-io/hygge/pkg/core"
	"gopkg.in/yaml.v3"
)

func LoadSpec(content []byte, obj any, override core.BaseLogger) error {
	logger := core.NewLogger("configurations.LoadSpec").IfNot(override)
	err := yaml.Unmarshal(content, obj)
	if err != nil {
		return logger.Wrapf(err, "cannot load object")
	}
	return nil
}

func SerializeSpec(spec any) ([]byte, error) {
	logger := core.NewLogger("configurations.SerializeSpec")
	content, err := yaml.Marshal(spec)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot serialize object")
	}
	return content, nil
}
