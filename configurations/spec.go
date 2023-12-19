package configurations

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"gopkg.in/yaml.v3"
)

func LoadSpec(ctx context.Context, content []byte, obj any) error {
	w := wool.Get(ctx).In("LoadSpec")
	err := yaml.Unmarshal(content, obj)
	if err != nil {
		return w.Wrapf(err, "cannot load object")
	}
	return nil
}

func SerializeSpec(ctx context.Context, spec any) ([]byte, error) {
	w := wool.Get(ctx).In("SerializeSpec")
	content, err := yaml.Marshal(spec)
	if err != nil {
		return nil, w.Wrapf(err, "cannot serialize object")
	}
	return content, nil
}
