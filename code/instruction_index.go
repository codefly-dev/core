package code

// ARCHITECTURE: instruction discovery and Markdown interpretation execute
// inside Codefly because repository guidance is project material. Callers get
// typed, scoped records plus body-free document identities; they never receive
// a complete instruction document merely to parse it in an orchestration
// process.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/yuin/goldmark"
	markdownast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"google.golang.org/protobuf/proto"
)

const (
	instructionAnalyzerVersion = "codefly.instruction-index/v1"
	maxInstructionDocumentSize = 1 << 20
	maxInstructionRecordSize   = 32 << 10
)

type instructionHeading struct {
	level     int
	title     string
	lineStart int
	lineEnd   int
	line      int32
	lineage   []string
}

// getInstructionIndex performs one deterministic scan through the server VFS.
// A source-local failure degrades the index without erasing records extracted
// from healthy sibling documents.
func (s *DefaultCodeServer) getInstructionIndex(ctx context.Context, _ *codev0.GetInstructionIndexRequest) (*codev0.CodeResponse, error) {
	index := &basev0.InstructionIndex{
		State:           basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_COMPLETE,
		Analyzer:        "codefly-core/goldmark",
		AnalyzerVersion: instructionAnalyzerVersion,
	}
	err := s.FS.WalkDir(s.SourceDir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != s.SourceDir {
				if _, err := s.FS.Stat(filepath.Join(filename, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			if filename != s.SourceDir && skipInstructionInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(s.SourceDir, filename)
		if err != nil {
			return fmt.Errorf("instruction index relative path %s: %w", filename, err)
		}
		relative = filepath.ToSlash(relative)
		if !isInstructionDocument(relative) {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			index.Issues = append(index.Issues, instructionIssue("symlink_unsupported", relative, "instruction document symlinks are not inspected"))
			return nil
		}
		info, err := s.FS.Stat(filename)
		if err != nil {
			index.Issues = append(index.Issues, instructionIssue("metadata_failed", relative, "instruction document metadata is unavailable"))
			return nil
		}
		if info.Size() > maxInstructionDocumentSize {
			index.Issues = append(index.Issues, instructionIssue("document_too_large", relative, fmt.Sprintf("instruction document exceeds %d-byte inspection limit", maxInstructionDocumentSize)))
			return nil
		}
		body, err := s.FS.ReadFile(filename)
		if err != nil {
			index.Issues = append(index.Issues, instructionIssue("read_failed", relative, "instruction document could not be read"))
			return nil
		}
		digest := sha256.Sum256(body)
		digestHex := hex.EncodeToString(digest[:])
		scopePath := instructionScopePath(relative)
		index.Documents = append(index.Documents, &basev0.InstructionDocument{
			Path: relative, ContentSha256: digestHex, ByteSize: int64(len(body)), ScopePath: scopePath,
		})
		records, issues := projectInstructionDocument(relative, scopePath, digestHex, body)
		index.Records = append(index.Records, records...)
		index.Issues = append(index.Issues, issues...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("instruction index: %w", err)
	}
	if len(index.Issues) > 0 {
		index.State = basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_DEGRADED
	}
	sort.Slice(index.Documents, func(i, j int) bool { return index.Documents[i].GetPath() < index.Documents[j].GetPath() })
	sort.Slice(index.Records, func(i, j int) bool {
		left, right := index.Records[i], index.Records[j]
		if left.GetSourcePath() != right.GetSourcePath() {
			return left.GetSourcePath() < right.GetSourcePath()
		}
		if left.GetStartLine() != right.GetStartLine() {
			return left.GetStartLine() < right.GetStartLine()
		}
		return left.GetId() < right.GetId()
	})
	sort.Slice(index.Issues, func(i, j int) bool {
		if index.Issues[i].GetPath() != index.Issues[j].GetPath() {
			return index.Issues[i].GetPath() < index.Issues[j].GetPath()
		}
		return index.Issues[i].GetCode() < index.Issues[j].GetCode()
	})
	index.Fingerprint = instructionIndexFingerprint(index)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetInstructionIndex{GetInstructionIndex: index}}, nil
}

func projectInstructionDocument(sourcePath, scopePath, sourceHash string, source []byte) ([]*basev0.InstructionKnowledge, []*basev0.InstructionIssue) {
	document := goldmark.New().Parser().Parse(text.NewReader(source))
	headings := instructionHeadings(document, source)
	if len(headings) == 0 {
		guidance := strings.TrimSpace(string(source))
		if guidance == "" {
			return nil, nil
		}
		if len(guidance) > maxInstructionRecordSize {
			return nil, []*basev0.InstructionIssue{instructionIssue("section_too_large", sourcePath, fmt.Sprintf("headingless instruction section exceeds %d-byte record limit", maxInstructionRecordSize))}
		}
		record := instructionRecord(sourcePath, scopePath, sourceHash, path.Base(sourcePath), guidance, 1, lineNumberAt(source, len(source)), basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_GUIDANCE)
		return []*basev0.InstructionKnowledge{record}, nil
	}

	records := make([]*basev0.InstructionKnowledge, 0, len(headings))
	issues := make([]*basev0.InstructionIssue, 0)
	for i, heading := range headings {
		bodyEnd := len(source)
		if i+1 < len(headings) {
			bodyEnd = headings[i+1].lineStart
		}
		bodyStart := heading.lineEnd
		for bodyStart < bodyEnd && isMarkdownSpace(source[bodyStart]) {
			bodyStart++
		}
		for bodyEnd > bodyStart && isMarkdownSpace(source[bodyEnd-1]) {
			bodyEnd--
		}
		if bodyStart == bodyEnd {
			continue
		}
		guidance := string(source[bodyStart:bodyEnd])
		if len(guidance) > maxInstructionRecordSize {
			issues = append(issues, instructionIssue("section_too_large", sourcePath, fmt.Sprintf("instruction section at line %d exceeds %d-byte record limit", heading.line, maxInstructionRecordSize)))
			continue
		}
		kind := classifyInstructionKnowledge(heading.lineage)
		records = append(records, instructionRecord(
			sourcePath, scopePath, sourceHash, heading.title, guidance,
			heading.line, lineNumberAt(source, bodyEnd), kind,
		))
	}
	return records, issues
}

func instructionHeadings(document markdownast.Node, source []byte) []instructionHeading {
	var headings []instructionHeading
	var stack []instructionHeading
	_ = markdownast.Walk(document, func(node markdownast.Node, entering bool) (markdownast.WalkStatus, error) {
		if !entering {
			return markdownast.WalkContinue, nil
		}
		heading, ok := node.(*markdownast.Heading)
		if !ok || heading.Lines().Len() == 0 {
			return markdownast.WalkContinue, nil
		}
		segment := heading.Lines().At(0)
		lineStart := markdownLineStart(source, segment.Start)
		lineEnd := markdownLineEnd(source, segment.Stop)
		title := strings.TrimSpace(string(heading.Lines().Value(source)))
		for len(stack) > 0 && stack[len(stack)-1].level >= heading.Level {
			stack = stack[:len(stack)-1]
		}
		lineage := make([]string, 0, len(stack)+1)
		for _, parent := range stack {
			lineage = append(lineage, parent.title)
		}
		lineage = append(lineage, title)
		projected := instructionHeading{
			level: heading.Level, title: title, lineStart: lineStart, lineEnd: lineEnd,
			line: lineNumberAt(source, lineStart), lineage: lineage,
		}
		headings = append(headings, projected)
		stack = append(stack, projected)
		return markdownast.WalkContinue, nil
	})
	return headings
}

func classifyInstructionKnowledge(lineage []string) basev0.InstructionKnowledgeKind {
	value := strings.ToLower(strings.Join(lineage, " "))
	switch {
	case containsInstructionTerm(value, "anti-pattern", "antipattern", "avoid", "don't", "do not", "what not to do"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_ANTI_PATTERN
	case containsInstructionTerm(value, "requirement", "required", "non-negotiable", "absolute", "must", "never"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_REQUIREMENT
	case containsInstructionTerm(value, "decision", "adr", "rationale"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_DECISION
	case containsInstructionTerm(value, "convention", "style", "pattern"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_CONVENTION
	case containsInstructionTerm(value, "lesson", "post-mortem", "postmortem"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_LESSON
	case containsInstructionTerm(value, "assumption"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_ASSUMPTION
	case containsInstructionTerm(value, "rule", "cardinal"):
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_RULE
	default:
		return basev0.InstructionKnowledgeKind_INSTRUCTION_KNOWLEDGE_KIND_GUIDANCE
	}
}

func instructionRecord(sourcePath, scopePath, sourceHash, title, guidance string, startLine, endLine int32, kind basev0.InstructionKnowledgeKind) *basev0.InstructionKnowledge {
	hash := sha256.New()
	for _, value := range []string{sourcePath, sourceHash, title, guidance, scopePath, kind.String()} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return &basev0.InstructionKnowledge{
		Id: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Kind: kind, Title: title, Guidance: guidance,
		SourcePath: sourcePath, SourceSha256: sourceHash, StartLine: startLine, EndLine: endLine, ScopePath: scopePath,
	}
}

func isInstructionDocument(relative string) bool {
	clean := path.Clean(filepath.ToSlash(relative))
	base := strings.ToUpper(path.Base(clean))
	switch base {
	case "AGENTS.MD", "CLAUDE.MD", "GEMINI.MD":
		return true
	}
	return strings.EqualFold(clean, ".github/copilot-instructions.md")
}

func instructionScopePath(relative string) string {
	if strings.EqualFold(path.Clean(relative), ".github/copilot-instructions.md") {
		return "."
	}
	scope := path.Dir(path.Clean(relative))
	if scope == "" {
		return "."
	}
	return scope
}

func skipInstructionInspectionDir(name string) bool {
	if strings.HasPrefix(name, ".") && !strings.EqualFold(name, ".github") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "target", "dist", "build", "__pycache__", "venv", "bin", "obj":
		return true
	default:
		return false
	}
}

func markdownLineStart(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	if index := bytes.LastIndexByte(source[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func markdownLineEnd(source []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		return len(source)
	}
	if index := bytes.IndexByte(source[offset:], '\n'); index >= 0 {
		return offset + index + 1
	}
	return len(source)
}

func lineNumberAt(source []byte, offset int) int32 {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	return int32(bytes.Count(source[:offset], []byte{'\n'}) + 1)
}

func isMarkdownSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func containsInstructionTerm(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func instructionIssue(code, sourcePath, message string) *basev0.InstructionIssue {
	return &basev0.InstructionIssue{Code: code, Path: sourcePath, Message: message}
}

func instructionIndexFingerprint(index *basev0.InstructionIndex) string {
	hash := sha256.New()
	for _, document := range index.GetDocuments() {
		for _, value := range []string{"document", document.GetPath(), document.GetContentSha256(), document.GetScopePath(), fmt.Sprintf("%d", document.GetByteSize())} {
			_, _ = hash.Write([]byte(value))
			_, _ = hash.Write([]byte{0})
		}
	}
	for _, record := range index.GetRecords() {
		_, _ = hash.Write([]byte("record"))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(record.GetId()))
		_, _ = hash.Write([]byte{0})
	}
	for _, issue := range index.GetIssues() {
		for _, value := range []string{"issue", issue.GetPath(), issue.GetCode(), issue.GetMessage()} {
			_, _ = hash.Write([]byte(value))
			_, _ = hash.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// FilterInstructionIndex returns the instruction hierarchy applicable at one
// canonical repository-relative scope. Repository-root guidance cascades into
// nested code units; sibling guidance never crosses between subtrees. The
// filtered result receives its own exact fingerprint.
func FilterInstructionIndex(index *basev0.InstructionIndex, scopePath string) *basev0.InstructionIndex {
	if index == nil {
		return nil
	}
	target := path.Clean(strings.TrimSpace(filepath.ToSlash(scopePath)))
	if target == "" {
		target = "."
	}
	out := proto.Clone(index).(*basev0.InstructionIndex)
	out.Documents = out.Documents[:0]
	for _, document := range index.GetDocuments() {
		if instructionScopeApplies(document.GetScopePath(), target) {
			out.Documents = append(out.Documents, proto.Clone(document).(*basev0.InstructionDocument))
		}
	}
	out.Records = out.Records[:0]
	for _, record := range index.GetRecords() {
		if instructionScopeApplies(record.GetScopePath(), target) {
			out.Records = append(out.Records, proto.Clone(record).(*basev0.InstructionKnowledge))
		}
	}
	out.Issues = out.Issues[:0]
	for _, issue := range index.GetIssues() {
		if instructionScopeApplies(instructionScopePath(issue.GetPath()), target) {
			out.Issues = append(out.Issues, proto.Clone(issue).(*basev0.InstructionIssue))
		}
	}
	if out.GetState() != basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_NOT_ATTEMPTED {
		out.State = basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_COMPLETE
		if len(out.GetIssues()) > 0 {
			out.State = basev0.InstructionIndexState_INSTRUCTION_INDEX_STATE_DEGRADED
		}
	}
	out.Fingerprint = instructionIndexFingerprint(out)
	return out
}

func instructionScopeApplies(documentScope, target string) bool {
	documentScope = path.Clean(strings.TrimSpace(filepath.ToSlash(documentScope)))
	if documentScope == "" || documentScope == "." {
		return true
	}
	target = path.Clean(strings.TrimSpace(filepath.ToSlash(target)))
	return target == documentScope || strings.HasPrefix(target, documentScope+"/")
}
