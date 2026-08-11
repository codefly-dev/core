package sensitive_test

import (
	"testing"

	"github.com/codefly-dev/core/internal/sensitive"
	"github.com/stretchr/testify/require"
)

func TestRedactTextProtectsHumanAndMachineSecretLabels(t *testing.T) {
	const unseal = "unseal-value-must-not-survive"
	const token = "root-token-must-not-survive"
	input := "Unseal Key: " + unseal + "\nROOT_TOKEN=" + token + "\nApi Address: http://127.0.0.1:8200\n"

	redacted := sensitive.RedactText(input)
	require.NotContains(t, redacted, unseal)
	require.NotContains(t, redacted, token)
	require.Contains(t, redacted, "Unseal Key: ****")
	require.Contains(t, redacted, "ROOT_TOKEN=****")
	require.Contains(t, redacted, "Api Address: http://127.0.0.1:8200")
}

func TestKeyCanonicalizesHumanReadableSeparators(t *testing.T) {
	for _, key := range []string{"unseal key", "api-key", "private.key", "database/url", "auth token"} {
		require.Truef(t, sensitive.Key(key), "key %q was not recognized", key)
	}
	require.False(t, sensitive.Key("api address"))
}
