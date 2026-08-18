package semantic

import (
	"encoding/hex"
	"strings"

	"github.com/codefly-dev/core/code"
)

// newSemanticServer builds a code server with the tree-sitter analyzer wired in,
// the configuration source-semantics consumers use.
func newSemanticServer(root string, opts ...code.ServerOption) *code.DefaultCodeServer {
	return code.NewDefaultCodeServer(root, append(opts, code.WithSemanticAnalyzer(New()))...)
}

// isCanonicalSHA256 reports whether value is a lowercase hex SHA-256 digest.
func isCanonicalSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
