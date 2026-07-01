package templates

import (
	"bytes"

	"github.com/codefly-dev/core/generation"
)

type ServiceReplacer struct {
	replacements []generation.Replacement
}

func NewServiceReplacer(gen *generation.Service) *ServiceReplacer {
	return &ServiceReplacer{replacements: gen.Replacements}
}

func (r *ServiceReplacer) Do(content []byte) ([]byte, error) {
	for _, replacement := range r.replacements {
		content = bytes.ReplaceAll(content, []byte(replacement.From), []byte(replacement.To))
	}
	return content, nil
}
