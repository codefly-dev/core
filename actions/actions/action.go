package actions

import (
	"context"
	"encoding/json"
	"fmt"
)

type Action interface {
	Run(ctx context.Context) (any, error)
	Command() string
}

var tracker *ActionTracker

func Run(ctx context.Context, action Action) (any, error) {
	res, err := action.Run(ctx)
	if err != nil {
		return nil, err
	}
	if tracker != nil {
		err := tracker.Save(action)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

func As[T any](t any) (*T, error) {
	if t.(*T) == nil {
		return nil, fmt.Errorf("cannot cast")
	}
	return t.(*T), nil
}

type BuilderFunc func(content []byte) (Action, error)

// Builder registry map
var builderRegistry = make(map[string]BuilderFunc)

func RegisterBuilder(typeName string, builder BuilderFunc) {
	builderRegistry[typeName] = builder
}

func CreateAction(content []byte) (Action, error) {
	var base Config
	err := json.Unmarshal(content, &base)
	if err != nil {
		return nil, err
	}
	typeName := base.Kind

	if builder, ok := builderRegistry[typeName]; ok {
		return builder(content)
	}
	return nil, fmt.Errorf("unknown type: %v", typeName)
}

// Wrap function with a constraint that *T satisfies Action
func Wrap[T Action]() BuilderFunc {
	return func(content []byte) (Action, error) {
		ptr, err := Load[T](content) // Note the pointer type here
		if err != nil {
			return nil, err
		}
		return *ptr, nil
	}
}

// Load function, *T must satisfy Action
func Load[T Action](content []byte) (*T, error) {
	var action T
	err := json.Unmarshal(content, &action)
	if err != nil {
		return nil, err
	}
	return &action, nil // Returning a pointer
}
