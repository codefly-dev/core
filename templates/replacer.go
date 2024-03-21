package templates

import (
	"bytes"

	"github.com/codefly-dev/core/configurations/generation"
)

type ServiceReplacer struct {
	replacements map[string]string
}

func NewServiceReplacer(gen *generation.Service) *ServiceReplacer {
	replacements := make(map[string]string)
	if gen.Base.Name != "" {
		replacements[gen.Base.Name] = "{{.Service.Name.Title}}"
	}
	return &ServiceReplacer{replacements: replacements}
}

func (r *ServiceReplacer) Do(content []byte) ([]byte, error) {
	for old, to := range r.replacements {
		content = bytes.ReplaceAll(content, []byte(old), []byte(to))
	}
	return content, nil
}
