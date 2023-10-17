package configurations

import (
	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

func LoadSpec(content []byte, obj any, override shared.BaseLogger) error {
	logger := shared.NewLogger("configurations.LoadSpec").IfNot(override)
	err := yaml.Unmarshal(content, obj)
	if err != nil {
		return logger.Wrapf(err, "cannot load object")
	}
	return nil
}

func SerializeSpec(spec any) ([]byte, error) {
	logger := shared.NewLogger("configurations.SerializeSpec")
	content, err := yaml.Marshal(spec)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot serialize object")
	}
	return content, nil
}
