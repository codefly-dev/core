package main

import (
	"github.com/codefly-dev/core/agents"
	providerv1 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v1"
)

type server struct {
	providerv1.UnimplementedProviderServer
}

func main() {
	agents.Serve(agents.PluginRegistration{Provider: &server{}})
}
