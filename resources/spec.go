package resources

import (
	"context"
	"encoding/json"
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

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

func ConvertToAnyPb(value any) (*anypb.Any, error) {
	// First, try direct conversion if it's already a proto.Message
	if msg, ok := value.(proto.Message); ok {
		return anypb.New(msg)
	}

	// If not, attempt to marshal to JSON and wrap in a GenericValue message
	jsonData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal value: %v", err)
	}

	genericValue := &basev0.GenericValue{
		Value: jsonData,
	}

	return anypb.New(genericValue)
}

func ConvertSpec(spec map[string]any) (*basev0.Specs, error) {
	stringAnyMap := &basev0.Specs{
		Fields: make(map[string]*basev0.SpecValue),
	}

	for key, value := range spec {
		v, err := ConvertToAnyPb(value)
		if err != nil {
			return nil, err
		}
		stringAnyMap.Fields[key] = &basev0.SpecValue{Value: v}
	}
	return stringAnyMap, nil
}

func FromAnyPb[T any](v *anypb.Any) (*T, error) {
	if v == nil {
		return nil, nil
	}
	// Attempt to unmarshal as a GenericValue
	genericValue := &basev0.GenericValue{}
	if err := v.UnmarshalTo(genericValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Any to GenericValue: %v", err)
	}
	var t T
	err := json.Unmarshal(genericValue.Value, &t)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal GenericValue: %v", err)
	}
	return &t, nil
}
