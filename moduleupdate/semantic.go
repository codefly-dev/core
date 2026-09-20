package moduleupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	SourceProtobuf         = "protobuf-descriptor/v1"
	SourceOpenAPIOperation = "openapi-operation/v1"
	SourceOpenAPISchema    = "openapi-schema/v1"
)

type ContractSource struct {
	Format  string
	Content []byte
}

type ContractEvidence struct {
	Snapshot *updatev0.ContractSnapshot
	Sources  map[string]ContractSource
}

type semanticChange struct {
	level  ChangeLevel
	reason string
}

func PrepareReleaseDiffWithSources(diff *updatev0.ReleaseDiff, before, after map[string]ContractSource) (*PreparedRelease, error) {
	prepared, err := PrepareReleaseDiff(diff)
	if err != nil {
		return nil, err
	}
	for _, pair := range []struct {
		sources map[string]ContractSource
		items   map[string]*updatev0.ContractItem
	}{{before, prepared.before}, {after, prepared.after}} {
		for _, id := range slices.Sorted(maps.Keys(pair.sources)) {
			source := pair.sources[id]
			item := pair.items[id]
			if item == nil || fmt.Sprintf("sha256:%x", sha256.Sum256(source.Content)) != item.Digest {
				return nil, fmt.Errorf("contract source %s does not match its snapshot digest", id)
			}
		}
	}
	prepared.semantic = make(map[string]semanticChange)
	for _, change := range prepared.diff.Changes {
		if change.Kind != updatev0.ChangeKind_CHANGE_KIND_MODIFIED {
			continue
		}
		old, oldOK := before[change.Item]
		next, nextOK := after[change.Item]
		if !oldOK || !nextOK || old.Format != next.Format {
			continue
		}
		result := compareContractSource(old, next)
		if result.level != ChangeMajor && !slices.Equal(prepared.before[change.Item].Dependencies, prepared.after[change.Item].Dependencies) {
			result = semanticChange{ChangeUndetermined, "changed dependency edges require additional consumer qualification"}
		}
		prepared.semantic[change.Item] = result
	}
	return prepared, nil
}

func compareContractSource(before, after ContractSource) semanticChange {
	if bytes.Equal(before.Content, after.Content) {
		return semanticChange{ChangePatch, "contract content is unchanged"}
	}
	switch before.Format {
	case SourceProtobuf:
		return compareProtobufSource(before.Content, after.Content)
	case SourceOpenAPIOperation:
		return compareOpenAPIOperation(before.Content, after.Content)
	case SourceOpenAPISchema:
		var old, next map[string]any
		if decodeContractJSON(before.Content, &old) != nil || decodeContractJSON(after.Content, &next) != nil {
			return semanticChange{ChangeUndetermined, "malformed schema evidence"}
		}
		return compareOpenAPISchema(old, next)
	}
	return semanticChange{ChangeUndetermined, "contract format has no supported semantic classifier"}
}

func decodeContractJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("contract evidence must contain exactly one JSON value")
	}
	return nil
}

func compareProtobufSource(before, after []byte) semanticChange {
	old, next := new(descriptorpb.FileDescriptorProto), new(descriptorpb.FileDescriptorProto)
	if protojson.Unmarshal(before, old) != nil || protojson.Unmarshal(after, next) != nil {
		return semanticChange{ChangeUndetermined, "malformed protobuf descriptor evidence"}
	}
	oldMessages, nextMessages := old.MessageType, next.MessageType
	old.MessageType, next.MessageType = nil, nil
	if !proto.Equal(old, next) || len(oldMessages) != 1 || len(nextMessages) != 1 {
		return semanticChange{ChangeUndetermined, "protobuf service, enum, file features or context changed"}
	}
	a, b := oldMessages[0], nextMessages[0]
	oldFields, nextFields := a.Field, b.Field
	a.Field, b.Field = nil, nil
	if !proto.Equal(a, b) {
		return semanticChange{ChangeUndetermined, "protobuf message options, oneofs or nested declarations changed"}
	}
	index := make(map[int32]*descriptorpb.FieldDescriptorProto)
	for _, field := range nextFields {
		if field.GetNumber() <= 0 || field.GetName() == "" || index[field.GetNumber()] != nil {
			return semanticChange{ChangeUndetermined, "invalid protobuf field identity"}
		}
		index[field.GetNumber()] = field
	}
	for _, field := range oldFields {
		candidate := index[field.GetNumber()]
		if candidate == nil {
			return semanticChange{ChangeMajor, "protobuf field was removed"}
		}
		if field.GetName() != candidate.GetName() || field.GetJsonName() != candidate.GetJsonName() || field.GetType() != candidate.GetType() || field.GetTypeName() != candidate.GetTypeName() || field.GetLabel() != candidate.GetLabel() {
			return semanticChange{ChangeMajor, "protobuf field identity, type or cardinality changed"}
		}
		if !proto.Equal(field, candidate) {
			return semanticChange{ChangeUndetermined, "protobuf field validation, presence, default or options changed"}
		}
		delete(index, field.GetNumber())
	}
	for _, number := range slices.Sorted(maps.Keys(index)) {
		field := index[number]
		if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REQUIRED {
			return semanticChange{ChangeMajor, "protobuf required field was added"}
		}
		if old.GetSyntax() != "proto3" || field.OneofIndex != nil || field.Options != nil || field.GetType() == descriptorpb.FieldDescriptorProto_TYPE_GROUP {
			return semanticChange{ChangeUndetermined, "new protobuf field has unsupported presence, validation or syntax"}
		}
	}
	if len(index) > 0 {
		return semanticChange{ChangeMinor, "optional protobuf fields were added"}
	}
	return semanticChange{ChangePatch, "protobuf field order changed without changing the contract"}
}

func compareOpenAPIOperation(before, after []byte) semanticChange {
	var old, next []map[string]any
	if decodeContractJSON(before, &old) != nil || decodeContractJSON(after, &next) != nil || len(old) != 2 || len(next) != 2 {
		return semanticChange{ChangeUndetermined, "malformed OpenAPI operation evidence"}
	}
	if !reflect.DeepEqual(old[0], next[0]) {
		return semanticChange{ChangeUndetermined, "OpenAPI path-level requirements changed"}
	}
	a, aOK := operationParameters(old[1])
	b, bOK := operationParameters(next[1])
	if !aOK || !bOK {
		return semanticChange{ChangeUndetermined, "unsupported OpenAPI parameter declarations"}
	}
	delete(old[1], "parameters")
	delete(next[1], "parameters")
	if !reflect.DeepEqual(old[1], next[1]) {
		return semanticChange{ChangeUndetermined, "OpenAPI responses, authorization or operation behavior changed"}
	}
	for _, key := range slices.Sorted(maps.Keys(a)) {
		parameter := a[key]
		candidate, exists := b[key]
		if !exists {
			return semanticChange{ChangeMajor, "OpenAPI parameter was removed"}
		}
		if !reflect.DeepEqual(parameter, candidate) {
			return semanticChange{ChangeUndetermined, "OpenAPI parameter schema or requirements changed"}
		}
		delete(b, key)
	}
	for _, key := range slices.Sorted(maps.Keys(b)) {
		parameter := b[key]
		if required, _ := parameter["required"].(bool); required {
			return semanticChange{ChangeMajor, "OpenAPI required parameter was added"}
		}
		if parameter["in"] == "path" {
			return semanticChange{ChangeUndetermined, "OpenAPI path parameter must be required"}
		}
	}
	if len(b) > 0 {
		return semanticChange{ChangeMinor, "optional OpenAPI parameters were added"}
	}
	return semanticChange{ChangePatch, "OpenAPI parameter ordering changed without changing requirements"}
}

func operationParameters(operation map[string]any) (map[string]map[string]any, bool) {
	result := make(map[string]map[string]any)
	raw, exists := operation["parameters"]
	if !exists {
		return result, true
	}
	parameters, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	for _, raw := range parameters {
		parameter, ok := raw.(map[string]any)
		if !ok || parameter["$ref"] != nil {
			return nil, false
		}
		name, _ := parameter["name"].(string)
		location, _ := parameter["in"].(string)
		if name == "" || !slices.Contains([]string{"query", "header", "path", "cookie"}, location) {
			return nil, false
		}
		if value, exists := parameter["required"]; exists {
			if _, ok := value.(bool); !ok {
				return nil, false
			}
		}
		if location == "path" && parameter["required"] != true {
			return nil, false
		}
		for name := range parameter {
			if !slices.Contains([]string{"name", "in", "required", "schema", "description"}, name) {
				return nil, false
			}
		}
		schema, ok := parameter["schema"].(map[string]any)
		if !ok || schema["default"] != nil {
			return nil, false
		}
		kind, _ := schema["type"].(string)
		if !slices.Contains([]string{"string", "integer", "number", "boolean"}, kind) {
			return nil, false
		}
		key := location + "\x00" + name
		if result[key] != nil {
			return nil, false
		}
		result[key] = parameter
	}
	return result, true
}

func compareOpenAPISchema(old, next map[string]any) semanticChange {
	if reflect.DeepEqual(old, next) {
		return semanticChange{ChangePatch, "schema is unchanged"}
	}
	oldType, _ := old["type"].(string)
	nextType, _ := next["type"].(string)
	if oldType != "" && nextType != "" && oldType != nextType {
		return semanticChange{ChangeMajor, "OpenAPI schema type changed"}
	}
	if oldType != "object" || nextType != "object" {
		return semanticChange{ChangeUndetermined, "changed scalar, union or referenced schema requires qualification"}
	}
	a, aOK := old["properties"].(map[string]any)
	b, bOK := next["properties"].(map[string]any)
	if !aOK || !bOK {
		return semanticChange{ChangeUndetermined, "object properties are incomplete"}
	}
	for _, name := range slices.Sorted(maps.Keys(a)) {
		property := a[name]
		candidate, exists := b[name]
		if !exists {
			return semanticChange{ChangeMajor, "OpenAPI object property was removed"}
		}
		if !reflect.DeepEqual(property, candidate) {
			oldProperty, oldOK := property.(map[string]any)
			newProperty, newOK := candidate.(map[string]any)
			if oldOK && newOK {
				if change := compareOpenAPISchema(oldProperty, newProperty); change.level == ChangeMajor {
					return change
				}
			}
			return semanticChange{ChangeUndetermined, "OpenAPI property validation or behavior changed"}
		}
	}
	return semanticChange{ChangeUndetermined, "OpenAPI object additions or requirements need direction-specific consumer expectations"}
}
