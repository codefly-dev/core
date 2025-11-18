package resources_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"buf.build/go/protovalidate"
)

func TestValidate(t *testing.T) {
	_, err := protovalidate.New()
	// Nothing
	require.NoError(t, err)
}
