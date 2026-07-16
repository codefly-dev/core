// Command conformance-toolbox runs the deterministic Toolbox conformance
// fixture as a real agent process through the standard secure startup helper.
package main

import (
	"github.com/codefly-dev/core/agents"
	coretoolbox "github.com/codefly-dev/core/toolbox"
	"github.com/codefly-dev/core/toolbox/conformance"
)

func main() {
	agents.ServeToolbox(conformance.New(coretoolbox.Version()))
}
