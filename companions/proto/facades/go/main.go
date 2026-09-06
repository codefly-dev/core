// Command protoc-gen-codefly-facade-go is a protoc plugin that emits a typed,
// gateway-bound client facade over the connect stubs protoc-gen-connect-go
// produces. For every proto package with services it writes one
// <module>_facade.pb.go in the connect stubs' parent Go package, wrapping each
// unary RPC so a handler passes and receives plain messages instead of the
// connect.Request/Response envelope. See facade.go for the generation logic and
// the plugin options (services, module, fail_on_streaming).
package main

import (
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-codefly-facade-go:", err)
		os.Exit(1)
	}
	var req pluginpb.CodeGeneratorRequest
	if err = proto.Unmarshal(in, &req); err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-codefly-facade-go:", err)
		os.Exit(1)
	}

	// The facade options (services, module, fail_on_streaming) share the
	// parameter string with protogen's own reserved options (paths, module, M).
	// `module` in particular collides — protogen rejects `module=` under
	// `paths=source_relative` — so parse and strip ours before protogen sees
	// the string.
	opts, cleaned := parseParams(req.GetParameter())
	req.Parameter = &cleaned

	gen, err := protogen.Options{}.New(&req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-codefly-facade-go:", err)
		os.Exit(1)
	}
	gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

	if err = generate(gen, opts); err != nil {
		gen.Error(err)
	}

	out, err := proto.Marshal(gen.Response())
	if err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-codefly-facade-go:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-codefly-facade-go:", err)
		os.Exit(1)
	}
}
