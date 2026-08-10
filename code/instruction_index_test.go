package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"google.golang.org/protobuf/proto"
)

func TestSourceToolingInstructionIndexProjectsNestedTypedGuidance(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, root, "AGENTS.md", `# Working Agreement
Use the production capability path.

## Absolute Rules
Every project operation stays behind Codefly.

### Testing
Use real infrastructure and committed cassettes.

## Avoid These Patterns
Do not parse project source in the orchestration brain.
`)
	writeInstructionFixture(t, root, "services/api/AGENTS.md", `# API Guidance
Keep transport contracts typed.
`)
	writeInstructionFixture(t, root, "services/web/AGENTS.md", `# Web Guidance
Keep sibling guidance in the web scope.
`)
	writeInstructionFixture(t, root, ".github/copilot-instructions.md", `# Review Guidance
Verify the exact changed capability boundary.
`)
	writeInstructionFixture(t, root, "node_modules/dependency/AGENTS.md", `# Rules
This dependency guidance must not be projected.
`)

	server := NewDefaultCodeServer(root)
	t.Cleanup(func() { _ = server.Close() })
	tooling := NewSourceTooling(server)
	response, err := tooling.GetInstructionIndex(t.Context(), &toolingv0.GetInstructionIndexRequest{})
	if err != nil {
		t.Fatalf("GetInstructionIndex: %v", err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("GetInstructionIndex failure: %+v", response.GetFailure())
	}
	index := response.GetIndex()
	if index.GetState() != basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_COMPLETE {
		t.Fatalf("state = %s, want COMPLETE; issues=%+v", index.GetState(), index.GetIssues())
	}
	if index.GetAnalyzer() != "codefly-core/goldmark" || index.GetAnalyzerVersion() != instructionAnalyzerVersion {
		t.Fatalf("analyzer provenance = %q %q", index.GetAnalyzer(), index.GetAnalyzerVersion())
	}
	if !strings.HasPrefix(index.GetFingerprint(), "sha256:") || len(index.GetFingerprint()) != 71 {
		t.Fatalf("projection fingerprint = %q", index.GetFingerprint())
	}
	if len(index.GetDocuments()) != 4 {
		t.Fatalf("documents = %d, want 4: %+v", len(index.GetDocuments()), index.GetDocuments())
	}
	for _, document := range index.GetDocuments() {
		if len(document.GetContentSha256()) != 64 || document.GetByteSize() == 0 {
			t.Fatalf("document identity is incomplete: %+v", document)
		}
	}
	if got := instructionDocumentScopes(index); got != ".github/copilot-instructions.md=.,AGENTS.md=.,services/api/AGENTS.md=services/api,services/web/AGENTS.md=services/web" {
		t.Fatalf("document scopes = %q", got)
	}

	records := instructionRecordsByTitle(index)
	assertInstructionRecord(t, records["Absolute Rules"], basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_REQUIREMENT, ".", "AGENTS.md")
	assertInstructionRecord(t, records["Testing"], basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_REQUIREMENT, ".", "AGENTS.md")
	assertInstructionRecord(t, records["Avoid These Patterns"], basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_ANTI_PATTERN, ".", "AGENTS.md")
	assertInstructionRecord(t, records["API Guidance"], basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_GUIDANCE, "services/api", "services/api/AGENTS.md")
	if strings.Contains(instructionRecordText(index), "dependency guidance") {
		t.Fatal("ignored dependency instruction escaped into the index")
	}
	apiIndex := FilterInstructionIndex(index, "services/api")
	if got := instructionDocumentScopes(apiIndex); got != ".github/copilot-instructions.md=.,AGENTS.md=.,services/api/AGENTS.md=services/api" {
		t.Fatalf("API-scoped documents = %q", got)
	}
	if strings.Contains(instructionRecordText(apiIndex), "sibling guidance") || apiIndex.GetFingerprint() == index.GetFingerprint() {
		t.Fatalf("API scope retained a sibling or the unfiltered fingerprint: %+v", apiIndex)
	}

	repeated, err := tooling.GetInstructionIndex(t.Context(), &toolingv0.GetInstructionIndexRequest{})
	if err != nil {
		t.Fatalf("repeat GetInstructionIndex: %v", err)
	}
	if !proto.Equal(index, repeated.GetIndex()) {
		t.Fatalf("instruction projection is not deterministic\nfirst: %v\nsecond: %v", index, repeated.GetIndex())
	}
}

func TestInstructionIndexDegradesOneOversizedSectionWithoutErasingSiblings(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, root, "AGENTS.md", "# Guidance\n"+strings.Repeat("x", maxInstructionRecordSize+1)+"\n")
	writeInstructionFixture(t, root, "nested/CLAUDE.md", "# Rules\nKeep the healthy record.\n")

	server := NewDefaultCodeServer(root)
	t.Cleanup(func() { _ = server.Close() })
	response, err := NewSourceTooling(server).GetInstructionIndex(t.Context(), &toolingv0.GetInstructionIndexRequest{})
	if err != nil {
		t.Fatalf("GetInstructionIndex: %v", err)
	}
	index := response.GetIndex()
	if index.GetState() != basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_DEGRADED {
		t.Fatalf("state = %s, want DEGRADED", index.GetState())
	}
	if len(index.GetIssues()) != 1 || index.GetIssues()[0].GetCode() != "section_too_large" || index.GetIssues()[0].GetPath() != "AGENTS.md" {
		t.Fatalf("issues = %+v", index.GetIssues())
	}
	if record := instructionRecordsByTitle(index)["Rules"]; record == nil || record.GetGuidance() != "Keep the healthy record." {
		t.Fatalf("healthy sibling record was lost: %+v", record)
	}
}

func writeInstructionFixture(t *testing.T, root, relative, body string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(filename, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func instructionDocumentScopes(index *basev0.InstructionIndex) string {
	parts := make([]string, 0, len(index.GetDocuments()))
	for _, document := range index.GetDocuments() {
		parts = append(parts, document.GetPath()+"="+document.GetScopePath())
	}
	return strings.Join(parts, ",")
}

func instructionRecordsByTitle(index *basev0.InstructionIndex) map[string]*basev0.InstructionKnowledge {
	records := make(map[string]*basev0.InstructionKnowledge, len(index.GetRecords()))
	for _, record := range index.GetRecords() {
		records[record.GetTitle()] = record
	}
	return records
}

func instructionRecordText(index *basev0.InstructionIndex) string {
	var body strings.Builder
	for _, record := range index.GetRecords() {
		body.WriteString(record.GetGuidance())
		body.WriteByte('\n')
	}
	return body.String()
}

func assertInstructionRecord(t *testing.T, record *basev0.InstructionKnowledge, kind basev0.InstructionKnowledgeKind, scope, source string) {
	t.Helper()
	if record == nil {
		t.Fatalf("record from %s is missing", source)
	}
	if record.GetKind() != kind || record.GetScopePath() != scope || record.GetSourcePath() != source || len(record.GetSourceSha256()) != 64 || !strings.HasPrefix(record.GetId(), "sha256:") || record.GetStartLine() <= 0 || record.GetEndLine() < record.GetStartLine() {
		t.Fatalf("record provenance/classification is incomplete: %+v", record)
	}
}
