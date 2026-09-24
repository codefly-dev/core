package services

import "testing"

func TestUnresolvedManifestValue(t *testing.T) {
	t.Parallel()
	passes := map[string]string{
		"nested JSON ending in }}": `MODULE_PRINCIPALS: '{"documents":{"queues":["datasource"],"tenant":"acme"}}'`,
		"deeply nested JSON":       `CONFIG: '{"a":{"b":{"c":1}}}'`,
		"JSON array of objects":    `CONFIG: '[{"a":{"b":1}},{"c":{}}]'`,
		"plain value":              `name: example-service`,
		"dollar without braces":    `value: $HOME`,
	}
	for name, value := range passes {
		if unresolvedManifestValue.MatchString(value) {
			t.Errorf("%s: %q flagged as an unresolved placeholder", name, value)
		}
	}
	fails := map[string]string{
		"helm values":        `name: {{ .Values.x }}`,
		"compact action":     `name: {{.x}}`,
		"trimmed action":     `name: {{- x -}}`,
		"function action":    `name: {{ include "chart.name" . }}`,
		"variable action":    `name: {{$name}}`,
		"comment action":     `name: {{/* todo */}}`,
		"unclosed action":    `name: {{ .Values.x`,
		"action inside JSON": `CONFIG: '{"tenant":"{{ .Tenant }}"}'`,
		"shell variable":     `name: ${FOO}`,
		"change me":          `password: CHANGE_ME`,
		"replace me":         `host: replace_me`,
	}
	for name, value := range fails {
		if !unresolvedManifestValue.MatchString(value) {
			t.Errorf("%s: %q not flagged as an unresolved placeholder", name, value)
		}
	}
}
