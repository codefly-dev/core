package golang_test

// These tests start containers and processes that read the identity of the run
// that launched them; a run surrounding the test must not be that run.
import _ "github.com/codefly-dev/core/internal/testenv"
