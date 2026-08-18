package code

// ARCHITECTURE: code-unit assembly is language-agnostic and stays here so the
// base server keeps working without the tree-sitter CGO stack. Language-manifest
// parsing (JVM/.NET, including Gradle's tree-sitter grammars) lives in the
// installed semantic analyzer.

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func (s *DefaultCodeServer) inspectDeclarativeProject(ctx context.Context) (*codev0.GetProjectInfoResponse, *basev0.Failure) {
	info := &codev0.GetProjectInfoResponse{FileHashes: ComputeFileHashes(s.FS, s.SourceDir, nil)}
	declarations, err := s.scanCodeUnitDeclarations(ctx)
	if err != nil {
		return info, failures.New(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.get-project-info", err.Error())
	}
	units := assembleCodeUnits(s.SourceDir, declarations)
	if len(units) != 1 || units[0].GetPath() != "." {
		return info, failures.New(
			basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED,
			"code.get-project-info",
			"project info requires one code-unit root; discover and inspect each unit independently",
		)
	}
	unit := units[0]
	info.Language = unit.GetPrimaryLanguage()
	switch unit.GetPrimaryLanguage() {
	case "jvm", "dotnet":
		if s.semantic == nil {
			return info, failures.New(
				basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION,
				"code.get-project-info",
				fmt.Sprintf("project inspection for %q requires a source-semantics analyzer", unit.GetPrimaryLanguage()),
			)
		}
		if err := s.semantic.InspectProject(ctx, s.FS, s.SourceDir, unit.GetPrimaryLanguage(), info); err != nil {
			return info, failures.New(basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED, "code.get-project-info", err.Error())
		}
		return info, nil
	default:
		return info, failures.New(
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION,
			"code.get-project-info",
			fmt.Sprintf("project inspection is unavailable in the generic agent for language %q", unit.GetPrimaryLanguage()),
		)
	}
}
