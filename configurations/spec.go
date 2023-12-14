package configurations

import (
	"fmt"

	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

func LoadSpec(content []byte, obj any) error {
	err := yaml.Unmarshal(content, obj)
	if err != nil {
		return fmt.Errorf("cannot unmarshal object: %w", err)
	}
	return nil
}

func SerializeSpec(spec any) ([]byte, error) {
	logger := shared.NewLogger().With("configurations.SerializeSpec")
	content, err := yaml.Marshal(spec)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot serialize object")
	}
	return content, nil
}
