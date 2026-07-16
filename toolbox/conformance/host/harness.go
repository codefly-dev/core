// Package host is the CLI-independent external-host conformance harness for
// Codefly Toolboxes. Mind and other hosts can import this package to prove the
// complete supported session path without depending on Codefly command wiring.
package host

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/codefly-dev/core/toolbox/conformance"
	"github.com/codefly-dev/core/toolbox/session"
)

// IdentityProofOptions supplies trusted session composition plus optional
// correlation identifiers. Session.Options owns authority; these IDs do not.
type IdentityProofOptions struct {
	Session     session.Options
	RequestID   string
	ObjectiveID string
	TaskID      string
}

// IdentityProof is deliberately evidence-only. It never returns arguments,
// model content, credentials, authorization tokens, or principal/tenant data.
type IdentityProof struct {
	ContractVersion string
	ToolboxName     string
	ToolboxVersion  string
	ToolNames       []string
	CatalogDigest   string
	RequestDigest   string
	InvocationID    string
	AuthorizationID string
	RequestID       string
	ObjectiveID     string
	TaskID          string
	TraceID         string
	ReleaseID       string
}

// RunIdentityProof performs the first CF1 demonstrable path: list summaries,
// describe the fixture identity tool, authorize an exact request, invoke the
// guarded process, validate its structured contract, and clean up.
func RunIdentityProof(ctx context.Context, options IdentityProofOptions) (_ *IdentityProof, returnErr error) {
	opened, err := session.Open(ctx, options.Session)
	if err != nil {
		return nil, fmt.Errorf("external host conformance: open: %w", err)
	}
	defer func() {
		if closeErr := opened.Close(); returnErr == nil && closeErr != nil {
			returnErr = fmt.Errorf("external host conformance: cleanup: %w", closeErr)
		}
	}()

	catalog := opened.Catalog()
	approved, err := opened.DescribeTool(ctx, conformance.IdentityTool)
	if err != nil {
		return nil, fmt.Errorf("external host conformance: describe: %w", err)
	}
	arguments, err := structpb.NewStruct(map[string]any{"subject": "external-host"})
	if err != nil {
		return nil, fmt.Errorf("external host conformance: arguments: %w", err)
	}
	result, err := opened.Call(ctx, session.CallRequest{
		Name: conformance.IdentityTool, Arguments: arguments,
		RequestID: options.RequestID, ObjectiveID: options.ObjectiveID, TaskID: options.TaskID,
	})
	if err != nil {
		return nil, fmt.Errorf("external host conformance: call: %w", err)
	}
	if result.Response == nil || len(result.Response.GetContent()) != 1 || result.Response.GetContent()[0].GetStructured() == nil {
		return nil, fmt.Errorf("external host conformance: identity result is not one structured content item")
	}
	payload := result.Response.GetContent()[0].GetStructured().AsMap()
	if payload["contract"] != conformance.ContractVersion {
		return nil, fmt.Errorf("external host conformance: contract=%v, want %s", payload["contract"], conformance.ContractVersion)
	}
	return &IdentityProof{
		ContractVersion: conformance.ContractVersion,
		ToolboxName:     catalog.Identity.GetName(), ToolboxVersion: catalog.Identity.GetVersion(),
		ToolNames: catalog.ToolNames(), CatalogDigest: approved.Digest,
		RequestDigest: result.RequestDigest, InvocationID: result.InvocationID,
		AuthorizationID: result.AuthorizationID, RequestID: result.RequestID,
		ObjectiveID: result.ObjectiveID, TaskID: result.TaskID, TraceID: result.TraceID,
		ReleaseID: result.ReleaseID,
	}, nil
}
