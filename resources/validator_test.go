package resources_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bufbuild/protovalidate-go"
)

func TestValidate(t *testing.T) {
	_, err := protovalidate.New()
	// Nothing
	require.NoError(t, err)
}
