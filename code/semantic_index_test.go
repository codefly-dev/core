package code

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSemanticIndexProjectsSupportedLanguagesWithoutBodies(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go/main.go": `package api
import "fmt"
const DefaultPrefix = "prefix-body-must-not-cross"
var Current = DefaultPrefix
type Server struct{}
func (s *Server) Handle(value string) string { return fmt.Sprint(value) }
`,
		"python/app.py": `import requests
class Client:
    def fetch(self, url: str):
        return requests.get(url, headers={"X-Body-Secret": "never-cross"})
`,
		"web/service.ts": `import {load} from "./loader";
export class Service { run(id: string): string { return load(id); } }
`,
		"jvm/Worker.java": `package demo.worker;
import java.time.Instant;
class Worker { Instant run() { return Instant.now(); } }
`,
		"jvm/Queue.kt": `package demo.queue
import java.util.UUID
class Queue
`,
		"dotnet/Cart.cs": `using System;
namespace Shop.Cart;
public class Cart { public string Id() { return Guid.NewGuid().ToString(); } }
`,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := NewDefaultCodeServer(root)
	response, err := server.Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}}})
	if err != nil {
		t.Fatal(err)
	}
	index := response.GetGetSemanticIndex()
	if index == nil || index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE {
		encoded, _ := protojson.Marshal(index)
		t.Fatalf("semantic index = %s, failure = %#v", encoded, response.GetFailure())
	}
	wantLanguages := []string{"csharp", "go", "java", "kotlin", "python", "typescript"}
	if !slices.Equal(index.GetLanguages(), wantLanguages) {
		t.Fatalf("languages = %v, want %v", index.GetLanguages(), wantLanguages)
	}
	for _, file := range index.GetFiles() {
		if file.GetPath() == "" || len(file.GetContentSha256()) != 64 || file.GetByteSize() == 0 {
			t.Fatalf("invalid semantic file: %#v", file)
		}
	}
	for _, want := range []string{"api.Server", "api.Server.Handle", "python.app.Client", "python.app.Client.fetch", "web.service.Service", "web.service.Service.run", "demo.worker.Worker", "demo.worker.Worker.run", "demo.queue.Queue", "Shop.Cart.Cart", "Shop.Cart.Cart.Id"} {
		if !hasSemanticQualifiedName(index, want) {
			t.Errorf("missing semantic symbol %q; got %v", want, semanticQualifiedNames(index))
		}
	}
	encoded, err := protojson.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "X-Body-Secret") || strings.Contains(string(encoded), "never-cross") {
		t.Fatalf("implementation body crossed semantic boundary: %s", encoded)
	}
	if strings.Contains(string(encoded), "prefix-body-must-not-cross") {
		t.Fatalf("data initializer crossed semantic boundary: %s", encoded)
	}
	for _, want := range []struct{ symbol, call string }{
		{symbol: "api.Server.Handle", call: "fmt.Sprint"},
		{symbol: "python.app.Client.fetch", call: "requests.get"},
		{symbol: "web.service.Service.run", call: "load"},
		{symbol: "demo.worker.Worker.run", call: "Instant.now"},
		{symbol: "Shop.Cart.Cart.Id", call: "Guid.NewGuid"},
	} {
		if !semanticSymbolHasCall(index, want.symbol, want.call) {
			t.Errorf("%s missing call %s", want.symbol, want.call)
		}
	}
	if got := semanticFile(index, "go/main.go").GetImports(); !slices.Equal(got, []string{"fmt"}) {
		t.Fatalf("Go imports = %v", got)
	}
	if got := semanticFile(index, "dotnet/Cart.cs").GetImports(); !slices.Equal(got, []string{"System"}) {
		t.Fatalf("C# imports = %v", got)
	}
}

func TestSemanticIndexReportsDegradedAndNotAttemptedCoverage(t *testing.T) {
	t.Run("degraded", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "broken.py"), []byte("def broken(:\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}}})
		if err != nil {
			t.Fatal(err)
		}
		index := response.GetGetSemanticIndex()
		if index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_DEGRADED || len(index.GetIssues()) != 1 || len(index.GetFiles()) != 1 {
			t.Fatalf("index = %#v", index)
		}
	})

	t.Run("not attempted", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "main.rb"), []byte("puts 'hello'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}}})
		if err != nil {
			t.Fatal(err)
		}
		index := response.GetGetSemanticIndex()
		if index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_NOT_ATTEMPTED || index.GetIssues()[0].GetCode() != "unsupported_source" {
			t.Fatalf("index = %#v", index)
		}
	})
}

func TestSemanticIndexExcludesLocalDataAndQualifiesNestedCallables(t *testing.T) {
	root := t.TempDir()
	body := `package main
func first() {
	var output string
	func() { _ = output }()
}
func second() {
	var output string
	_ = output
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	python := "def outer():\n    local = 1\n    def inner():\n        return local\n    return inner()\n"
	if err := os.WriteFile(filepath.Join(root, "nested.py"), []byte(python), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}}})
	if err != nil {
		t.Fatal(err)
	}
	index := response.GetGetSemanticIndex()
	if index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE {
		t.Fatalf("index = %#v", index)
	}
	if hasSemanticQualifiedName(index, "main.output") || hasSemanticQualifiedName(index, "nested.local") {
		t.Fatalf("local variable crossed declaration boundary: %v", semanticQualifiedNames(index))
	}
	if !hasSemanticQualifiedName(index, "main.first") || !hasSemanticQualifiedName(index, "main.second") {
		t.Fatalf("top-level callables missing: %v", semanticQualifiedNames(index))
	}
	if !hasSemanticQualifiedName(index, "nested.outer.inner") {
		t.Fatalf("nested callable has no enclosing identity: %v", semanticQualifiedNames(index))
	}
}

func TestSourceToolingDelegatesSemanticIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err := NewSourceTooling(NewDefaultCodeServer(root)).GetSemanticIndex(t.Context(), &toolingv0.GetSemanticIndexRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetFailure() != nil || response.GetIndex().GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE || !hasSemanticQualifiedName(response.GetIndex(), "main.main") {
		t.Fatalf("tooling response = %#v", response)
	}
}

func semanticFile(index *basev0.SemanticIndex, path string) *basev0.SemanticFile {
	for _, file := range index.GetFiles() {
		if file.GetPath() == path {
			return file
		}
	}
	return nil
}

func hasSemanticQualifiedName(index *basev0.SemanticIndex, name string) bool {
	for _, symbol := range index.GetSymbols() {
		if symbol.GetQualifiedName() == name {
			return true
		}
	}
	return false
}

func semanticQualifiedNames(index *basev0.SemanticIndex) []string {
	result := make([]string, 0, len(index.GetSymbols()))
	for _, symbol := range index.GetSymbols() {
		result = append(result, symbol.GetQualifiedName())
	}
	return result
}

func semanticSymbolHasCall(index *basev0.SemanticIndex, qualifiedName, call string) bool {
	for _, symbol := range index.GetSymbols() {
		if symbol.GetQualifiedName() != qualifiedName {
			continue
		}
		for _, use := range symbol.GetCalls() {
			if use.GetName() == call {
				return true
			}
		}
	}
	return false
}
