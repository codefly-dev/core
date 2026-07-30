package main

import (
	"github.com/codefly-dev/core/agents"
	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

type server struct {
	providerv0.UnimplementedProviderServer
}

func main() {
	agents.Serve(agents.PluginRegistration{Provider: &server{}})
}
