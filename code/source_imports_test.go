package code

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInspectSourceImportsUsesRealLanguageParsers(t *testing.T) {
	tests := []struct {
		name     string
		language string
		file     string
		source   string
		want     []string
	}{
		{name: "go", language: "go", file: "main.go", source: "package main\nimport (\"context\"; alias \"example.com/lib/sub\")\n", want: []string{"context", "example.com/lib/sub"}},
		{name: "python", language: "python", file: "app.py", source: "import flask, numpy.linalg as la\nfrom fastapi.routing import APIRouter\nfrom .local import value\n", want: []string{"fastapi.routing", "flask", "numpy.linalg"}},
		{name: "typescript", language: "typescript", file: "app.ts", source: "import express from 'express';\nexport {z} from 'zod';\nconst x = require('pino');\nimport local from './local';\n", want: []string{"express", "pino", "zod"}},
		{name: "java", language: "jvm", file: "src/App.java", source: "package app;\nimport static io.grpc.Status.OK;\nimport org.apache.logging.log4j.Logger;\nclass App {}\n", want: []string{"io.grpc.Status.OK", "org.apache.logging.log4j.Logger"}},
		{name: "kotlin", language: "jvm", file: "src/App.kt", source: "package app\nimport io.grpc.ServerBuilder\nimport org.slf4j.Logger as Log\nclass App\n", want: []string{"io.grpc.ServerBuilder", "org.slf4j.Logger"}},
		{name: "csharp", language: "dotnet", file: "src/App.cs", source: "global using Microsoft.AspNetCore.Hosting;\nusing static Grpc.Core.Status;\nusing Cache = Microsoft.Extensions.Caching.Distributed;\n", want: []string{"Grpc.Core.Status", "Microsoft.AspNetCore.Hosting", "Microsoft.Extensions.Caching.Distributed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			filename := filepath.Join(root, filepath.FromSlash(test.file))
			if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte(test.source), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := inspectSourceImports(context.Background(), LocalVFS{}, root, test.language)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].GetPath() != test.file || !reflect.DeepEqual(got[0].GetImports(), test.want) {
				t.Fatalf("source imports = %+v, want path %q imports %#v", got, test.file, test.want)
			}
		})
	}
}

func TestInspectSourceImportsRejectsMalformedSyntax(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.py"), []byte("from import\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectSourceImports(t.Context(), LocalVFS{}, root, "python"); err == nil {
		t.Fatal("malformed source was certified as complete")
	}
}
