package configurations

import (
	"context"
	"fmt"
	"os"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
	"gopkg.in/yaml.v3"
)

func ConfigurationInformationFromYaml(ctx context.Context, name string, p string, isSecret bool) (*basev0.ConfigurationInformation, error) {

	w := wool.Get(ctx).In("provider.ConfigurationInformationFromYaml")
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read yaml env file")
	}
	info := &basev0.ConfigurationInformation{
		Name: name,
	}

	var data map[string]interface{}
	err = yaml.Unmarshal(content, &data)
	if err != nil {
		return nil, w.Wrapf(err, "Error unmarshalling YAML: %s data: %s", p, string(content))
	}

	flattened := make(map[string]string)
	flattenMap("", data, flattened)

	for key, value := range flattened {
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{
			Key:    key,
			Value:  value,
			Secret: isSecret,
		})
	}
	return info, nil
}

func flattenMap(prefix string, m map[string]interface{}, result map[string]string) {
	for k, v := range m {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}

		switch vv := v.(type) {
		case map[string]interface{}:
			flattenMap(newKey, vv, result)
		default:
			result[newKey] = fmt.Sprintf("%v", v)
		}
	}
}

func configValuesToFlatMap(configValues []*basev0.ConfigurationValue) map[string]string {
	flat := make(map[string]string)
	for _, cv := range configValues {
		flat[cv.Key] = cv.Value
	}
	return flat
}

func unflattenMap(flat map[string]string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for k, v := range flat {
		keys := strings.Split(k, ".")
		current := result

		for i, key := range keys {
			if i == len(keys)-1 {
				current[key] = v
			} else {
				if _, exists := current[key]; !exists {
					current[key] = make(map[string]interface{})
				}
				next, ok := current[key].(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("conflicting types for key: %s", strings.Join(keys[:i+1], "."))
				}
				current = next
			}
		}
	}

	return result, nil
}

func injectIntoStruct(unflattened map[string]interface{}, v interface{}) error {
	yamlData, err := yaml.Marshal(unflattened)
	if err != nil {
		return fmt.Errorf("error marshalling unflattened map: %w", err)
	}

	return yaml.Unmarshal(yamlData, v)
}

func InformationUnmarshal(info *basev0.ConfigurationInformation, v interface{}) error {
	flatMap := configValuesToFlatMap(info.ConfigurationValues)
	unflattened, err := unflattenMap(flatMap)
	if err != nil {
		return fmt.Errorf("error unflattening map: %w", err)
	}

	return injectIntoStruct(unflattened, v)
}
