package resources

import (
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func TestConfigurationSummaryRedactsSensitiveKeyWithoutMetadata(t *testing.T) {
	value := &basev0.ConfigurationValue{Key: "CLICKHOUSE_PASSWORD", Value: "hunter2"}
	if got := MakeConfigurationValueSummary(value); got != "CLICKHOUSE_PASSWORD=****" {
		t.Fatalf("summary = %q", got)
	}
	if got := MakeConfigurationValueSummary(&basev0.ConfigurationValue{Key: "HOST", Value: "localhost"}); got != "HOST=localhost" {
		t.Fatalf("safe summary = %q", got)
	}
}
