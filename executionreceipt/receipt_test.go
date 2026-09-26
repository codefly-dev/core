package executionreceipt

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	executionv1 "github.com/codefly-dev/core/generated/go/codefly/execution/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAttestVerifyAndDeterministicDigest(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x2a}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	receipt := validReceipt()
	attestation, err := Attest(receipt, "gateway-installation-1", "gateway-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.GetPayloadSha256() != "" {
		t.Fatal("Attest mutated caller receipt")
	}
	verified, err := Verify(attestation, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if verified.GetPayloadSha256() == "" {
		t.Fatal("verified receipt has no payload digest")
	}
	if got := verified.GetPayloadSha256(); got != "ab10697bb28e525ba3ab0bdc675b46e9dc0a99be2ba0ef81239f76dc0f10b8f2" {
		t.Fatalf("payload_sha256 golden = %s", got)
	}
	if got := hex.EncodeToString(attestation.GetSignature()); got != "c0bb5538c1a163e548fd572f68da9786ebf5013eff748b5a1030cb3f67f9dd6db267ec9186cb92c6068a79632c97d854e5dd9e97f7e299e9ac0242f02de8050b" {
		t.Fatalf("signature golden = %s", got)
	}

	second, err := Attest(receipt, "gateway-installation-1", "gateway-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(attestation, second) {
		t.Fatal("byte-identical receipt was not attested deterministically")
	}
}

func TestVerifyRejectsImmutableMutationAndWrongKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := Attest(validReceipt(), "gateway-installation-1", "gateway-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Receipt.OperationKind = "test.run"
	if _, err := Verify(attestation, publicKey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutated receipt error = %v", err)
	}

	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Attest(validReceipt(), "gateway-installation-1", "gateway-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(fresh, otherPublic); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong-key error = %v", err)
	}
}

func TestPrepareRejectsLifecycleAndExtensionInconsistency(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*executionv1.ExecutionReceiptV1)
	}{
		{
			name: "terminal missing completed time",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.CompletedAt = nil
			},
		},
		{
			name: "nonterminal has completed time",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.Stage = executionv1.ExecutionStage_EXECUTION_STAGE_STARTED
			},
		},
		{
			name: "completed before started",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.CompletedAt = timestamppb.New(receipt.StartedAt.AsTime().Add(-time.Second))
			},
		},
		{
			name: "extension schema without payload",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				schema := "codefly.test-result/v1"
				receipt.ExtensionSchema = &schema
			},
		},
		{
			name: "duplicate resource",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.Resources = append(receipt.Resources, proto.Clone(receipt.Resources[0]).(*executionv1.ExecutionResourceV1))
			},
		},
		{
			name: "noncanonical operation kind",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.OperationKind = "ApplyEdit"
			},
		},
		{
			name: "operation segment starts with hyphen",
			mutate: func(receipt *executionv1.ExecutionReceiptV1) {
				receipt.OperationKind = "code.-apply"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := validReceipt()
			test.mutate(receipt)
			if _, err := Prepare(receipt); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Prepare error = %v", err)
			}
		})
	}
}

func TestPrepareRejectsCallerSuppliedConflictingDigest(t *testing.T) {
	receipt := validReceipt()
	receipt.PayloadSha256 = string(make([]byte, sha256.Size*2))
	if _, err := Prepare(receipt); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare error = %v", err)
	}
}

func validReceipt() *executionv1.ExecutionReceiptV1 {
	started := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	completed := started.Add(725 * time.Millisecond)
	workspaceID := "workspace-codefly"
	projectID := "project-warden"
	parentSessionID := "session-root"
	agentManifestID := "codefly.dev/claude:1.0.0"
	before := sha256.Sum256([]byte("before"))
	after := sha256.Sum256([]byte("after"))
	return &executionv1.ExecutionReceiptV1{
		Schema:        SchemaV1,
		ReceiptId:     "receipt-1",
		OperationId:   "operation-1",
		AttemptId:     "attempt-1",
		Stage:         executionv1.ExecutionStage_EXECUTION_STAGE_SUCCEEDED,
		OperationKind: "code.apply-edit",
		Producer: &executionv1.ExecutionProducerV1{
			Id: "codefly.execution", Component: "gateway", Release: "v0.1.23",
		},
		Assurance: executionv1.ExecutionAssurance_EXECUTION_ASSURANCE_PLUGIN_EXECUTED,
		WorkContext: &basev0.WorkContextV1{
			Typ: "codefly.work-context/v1", Algorithm: "Ed25519",
			KeyId: "accounts-key-1", Issuer: "accounts", Audience: "codefly.execution",
			NotBeforeUnix: started.Add(-time.Minute).Unix(), IssuedAtUnix: started.Add(-time.Minute).Unix(),
			ExpiresAtUnix: started.Add(4 * time.Minute).Unix(), Nonce: "nonce-1",
			AuthorizationRevision: 4, ReplayPolicy: "idempotent",
			TenantId: "tenant-codefly", OwnerPrincipalId: "principal-antoine",
			OwnerPrincipalKind: "human",
			TaskId:             "task-1", SessionId: "session-child", ParentSessionId: &parentSessionID,
			AuthorityScopes: []*basev0.WorkScopeV1{{
				ResourceKind: "evidence", Actions: []string{"append"}, ResourceIds: []string{"codefly.execution"},
			}},
			ActorChain: []*basev0.WorkActorV1{{
				PrincipalId: "principal-claude", PrincipalKind: "agent", AgentId: &agentManifestID, DelegationId: "delegation-1",
				GrantedScopes: []*basev0.WorkScopeV1{{
					ResourceKind: "evidence", Actions: []string{"append"}, ResourceIds: []string{"codefly.execution"},
				}},
			}},
			AttributionTeamIds: []string{"team-platform"}, WorkspaceId: &workspaceID, ProjectId: &projectID,
		},
		WorkContextSha256: hex.EncodeToString(before[:]),
		Target: &executionv1.ExecutionTargetV1{
			WorkspaceId: workspaceID, Service: "warden", ProjectId: &projectID,
		},
		StartedAt:   timestamppb.New(started),
		CompletedAt: timestamppb.New(completed),
		Resources: []*executionv1.ExecutionResourceV1{{
			Kind: "workspace.path", Reference: "modules/warden/main.go",
			BeforeSha256: stringPointer(hex.EncodeToString(before[:])),
			AfterSha256:  stringPointer(hex.EncodeToString(after[:])),
			Changed:      true,
		}},
		Result: &executionv1.ExecutionResultV1{
			Status: "passed", DurationMs: 725, PassedCount: 1,
		},
	}
}

func stringPointer(value string) *string {
	return &value
}
