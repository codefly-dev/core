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
	// The two goldens below MOVED when WorkContextV1's seal and actor epochs
	// became schema-required: a receipt embeds the capability as presented, so
	// the embedded seal is part of the bytes that are digested and signed.
	// They are re-pinned deliberately, not adjusted to make a test pass — the
	// receipt content genuinely changed, and any consumer holding these values
	// re-pins too. Archived receipts whose embedded capability carries no seal
	// no longer validate; that is the accepted cost of the field being
	// required, and such receipts are historical data that need a snapshot
	// type rather than a permanently weaker live credential.
	verified, err := Verify(attestation, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if verified.GetPayloadSha256() == "" {
		t.Fatal("verified receipt has no payload digest")
	}
	if got := verified.GetPayloadSha256(); got != "0593ffb8d984aa07423d8f611149a3c96bb7403008f387aa18f2e6957714b2b0" {
		t.Fatalf("payload_sha256 golden = %s", got)
	}
	if got := hex.EncodeToString(attestation.GetSignature()); got != "12c063e4afd86dc12427d9ac21c596fe0d57241e2702dd9bdcb79406d13a34a5922955de91ea370c983ae34463233a0251fb8e666a0dab9a67a6c76a24ff2304" {
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
			TenantId: "tenant-codefly", OwnerPrincipalId: "principal-owner",
			TaskId: "task-1", SessionId: "session-child", ParentSessionId: &parentSessionID,
			AuthorityScopes: []*basev0.WorkScopeV1{{
				ResourceKind: "evidence", Actions: []string{"append"}, ResourceIds: []string{"codefly.execution"},
			}},
			// The seal and every hop's epoch are required by the schema. A
			// receipt embeds the capability as it was presented, so a receipt
			// of a sealed capability carries the seal — which is the point of
			// making the field required rather than verifier-enforced: a
			// reader of a receipt sees the installation the authority was held
			// through, instead of seeing it as optional.
			Seal: &basev0.WorkSealV1{
				PrincipalEpoch: 2, InstallationId: "installation-1",
				InstallationRevision: 3, BuildIncarnation: 11,
			},
			ActorChain: []*basev0.WorkActorV1{{
				PrincipalId: "principal-claude", PrincipalKind: "agent", DelegationId: "delegation-1",
				PrincipalEpoch: 1,
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
