package code

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestDefaultCodeServerInspectsGradleProjectWithoutExecutingBuildTool(t *testing.T) {
	root := t.TempDir()
	writeProjectInspectionFile(t, root, "settings.gradle", "rootProject.name = 'adservice'\n")
	writeProjectInspectionFile(t, root, "build.gradle", `
group = "example.ads"
def grpcVersion = "1.82.1"
sourceCompatibility = JavaVersion.VERSION_21
dependencies {
    implementation "io.grpc:grpc-protobuf:${grpcVersion}",
                   "io.grpc:grpc-stub:${grpcVersion}"
    runtimeOnly "org.apache.logging.log4j:log4j-core:2.26.1"
}
`)
	writeProjectInspectionFile(t, root, "src/main/java/example/Ads.java", `package example;
import io.grpc.Server;
import org.apache.logging.log4j.Logger;
class Ads {}
`)

	response, err := NewDefaultCodeServer(root).Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("project failure = %+v", response.GetFailure())
	}
	info := response.GetGetProjectInfo()
	if info.GetLanguage() != "jvm" || info.GetModule() != "example.ads" || info.GetLanguageVersion() != "21" {
		t.Fatalf("identity = language %q module %q version %q", info.GetLanguage(), info.GetModule(), info.GetLanguageVersion())
	}
	wantDependencies := []*codev0.Dependency{
		{Name: "io.grpc:grpc-protobuf", Version: "1.82.1", Direct: true},
		{Name: "io.grpc:grpc-stub", Version: "1.82.1", Direct: true},
		{Name: "org.apache.logging.log4j:log4j-core", Version: "2.26.1", Direct: true},
	}
	if !reflect.DeepEqual(info.GetDependencies(), wantDependencies) {
		t.Fatalf("dependencies = %+v, want %+v", info.GetDependencies(), wantDependencies)
	}
	if len(info.GetSourceFiles()) != 1 || !reflect.DeepEqual(info.GetSourceFiles()[0].GetImports(), []string{"io.grpc.Server", "org.apache.logging.log4j.Logger"}) {
		t.Fatalf("source files = %+v", info.GetSourceFiles())
	}
}

func TestDefaultCodeServerInspectsDotNetSolutionProjects(t *testing.T) {
	root := t.TempDir()
	writeProjectInspectionFile(t, root, "cartservice.sln", "Microsoft Visual Studio Solution File, Format Version 12.00\n")
	writeProjectInspectionFile(t, root, "src/cartservice.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Grpc.AspNetCore" Version="2.80.0" />
    <PackageReference Include="Npgsql"><Version>10.0.3</Version></PackageReference>
  </ItemGroup>
</Project>`)
	writeProjectInspectionFile(t, root, "tests/cartservice.tests.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="xunit" Version="2.9.3" /></ItemGroup></Project>`)
	writeProjectInspectionFile(t, root, "src/Program.cs", "using Grpc.Core;\nusing Microsoft.AspNetCore.Hosting;\nclass Program {}\n")

	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("project failure = %+v", response.GetFailure())
	}
	info := response.GetGetProjectInfo()
	if info.GetLanguage() != "dotnet" || info.GetModule() != "cartservice" || info.GetLanguageVersion() != "net10.0" {
		t.Fatalf("identity = language %q module %q version %q", info.GetLanguage(), info.GetModule(), info.GetLanguageVersion())
	}
	want := []string{"Grpc.AspNetCore@2.80.0", "Npgsql@10.0.3", "xunit@2.9.3"}
	var got []string
	for _, dependency := range info.GetDependencies() {
		got = append(got, dependency.GetName()+"@"+dependency.GetVersion())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependencies = %#v, want %#v", got, want)
	}
	if len(info.GetSourceFiles()) != 1 || !reflect.DeepEqual(info.GetSourceFiles()[0].GetImports(), []string{"Grpc.Core", "Microsoft.AspNetCore.Hosting"}) {
		t.Fatalf("source files = %+v", info.GetSourceFiles())
	}
}

func TestDefaultCodeServerReportsUnsupportedInspectionAsTypedFailure(t *testing.T) {
	root := t.TempDir()
	writeProjectInspectionFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure := response.GetFailure(); failure.GetCode() != basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION || failure.GetRetryable() {
		t.Fatalf("failure = %+v, want non-retryable unsupported", failure)
	}
	if response.GetGetProjectInfo().GetLanguage() != "ruby" {
		t.Fatalf("language evidence was dropped: %+v", response.GetGetProjectInfo())
	}
}

func writeProjectInspectionFile(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
