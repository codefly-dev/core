package code

// ARCHITECTURE: source attributes are derived inside Codefly because path
// conventions and byte classification are project interpretation. Higher-level
// orchestrators receive versioned facts and never repeat this logic.

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

const sourceAttributesClassifierVersion = "codefly-source-attributes/v1"

func classifySourceAttributes(name string, kind basev0.SourceEntryKind, body []byte, bodyKnown bool) *basev0.SourceFileAttributes {
	language, languageReason := sourceLanguageForPath(name)
	contentKind, contentReason := sourceContentKind(name, kind, body, bodyKnown, language)
	role, roleReason, confidence := sourceRoleForPath(name, language)
	return &basev0.SourceFileAttributes{
		SchemaVersion:     1,
		Language:          language,
		ContentKind:       contentKind,
		SourceRole:        role,
		ClassifierVersion: sourceAttributesClassifierVersion,
		Confidence:        float32(confidence),
		Reason:            languageReason + "; " + contentReason + "; " + roleReason,
	}
}

func sourceLanguageForPath(name string) (basev0.SourceLanguage, string) {
	switch strings.ToLower(path.Ext(name)) {
	case ".go":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_GO, "extension .go"
	case ".py", ".pyi":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_PYTHON, "python extension"
	case ".ts", ".tsx", ".mts", ".cts":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_TYPESCRIPT, "TypeScript extension"
	case ".js", ".jsx", ".mjs", ".cjs":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_JAVASCRIPT, "JavaScript extension"
	case ".rs":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_RUST, "extension .rs"
	case ".java":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_JAVA, "extension .java"
	case ".kt", ".kts":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_KOTLIN, "Kotlin extension"
	case ".cs":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_CSHARP, "C# extension"
	case ".sql":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_SQL, "extension .sql"
	case ".json", ".jsonc":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_JSON, "JSON extension"
	case ".yaml", ".yml":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_YAML, "YAML extension"
	case ".toml":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_TOML, "extension .toml"
	case ".xml":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_XML, "extension .xml"
	case ".md", ".mdx", ".rst":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_MARKDOWN, "documentation extension"
	case ".sh", ".bash", ".zsh":
		return basev0.SourceLanguage_SOURCE_LANGUAGE_SHELL, "shell extension"
	default:
		return basev0.SourceLanguage_SOURCE_LANGUAGE_UNKNOWN, "no registered language extension"
	}
}

func sourceContentKind(name string, kind basev0.SourceEntryKind, body []byte, bodyKnown bool, language basev0.SourceLanguage) (basev0.SourceContentKind, string) {
	switch kind {
	case basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK:
		return basev0.SourceContentKind_SOURCE_CONTENT_KIND_SYMLINK, "source entry is a symlink"
	case basev0.SourceEntryKind_SOURCE_ENTRY_KIND_GITLINK:
		return basev0.SourceContentKind_SOURCE_CONTENT_KIND_GITLINK, "source entry is a gitlink"
	}
	if knownBinarySourceExtension(name) {
		return basev0.SourceContentKind_SOURCE_CONTENT_KIND_BINARY, "registered binary extension"
	}
	if bodyKnown {
		if bytes.IndexByte(body, 0) >= 0 || !utf8.Valid(body) {
			return basev0.SourceContentKind_SOURCE_CONTENT_KIND_BINARY, "content contains NUL or invalid UTF-8"
		}
		return basev0.SourceContentKind_SOURCE_CONTENT_KIND_TEXT, "verified UTF-8 content"
	}
	if language != basev0.SourceLanguage_SOURCE_LANGUAGE_UNKNOWN || knownTextSourceName(name) {
		return basev0.SourceContentKind_SOURCE_CONTENT_KIND_TEXT, "registered text extension or filename"
	}
	return basev0.SourceContentKind_SOURCE_CONTENT_KIND_UNKNOWN, "content bytes were not requested"
}

func sourceRoleForPath(name string, language basev0.SourceLanguage) (basev0.SourceRole, string, float64) {
	lower := strings.ToLower(strings.Trim(name, "/"))
	base := path.Base(lower)
	segments := "/" + lower + "/"
	switch {
	case strings.Contains(segments, "/vendor/") || strings.Contains(segments, "/node_modules/") || strings.Contains(segments, "/third_party/"):
		return basev0.SourceRole_SOURCE_ROLE_VENDOR, "vendor directory convention", 1
	case strings.Contains(segments, "/generated/") || strings.Contains(segments, "/gen/") || strings.Contains(base, ".generated.") || strings.HasSuffix(base, ".pb.go") || strings.HasSuffix(base, "_generated.go"):
		return basev0.SourceRole_SOURCE_ROLE_GENERATED, "generated path/name convention", .95
	case strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.HasPrefix(base, "test_") || strings.Contains(segments, "/test/") || strings.Contains(segments, "/tests/") || strings.Contains(segments, "/__tests__/"):
		return basev0.SourceRole_SOURCE_ROLE_TEST, "test path/name convention", .95
	case strings.Contains(segments, "/migrations/") || strings.Contains(segments, "/migration/") || strings.HasSuffix(base, ".up.sql") || strings.HasSuffix(base, ".down.sql"):
		return basev0.SourceRole_SOURCE_ROLE_MIGRATION, "migration path/name convention", .95
	case strings.Contains(segments, "/fixtures/") || strings.Contains(segments, "/testdata/") || strings.Contains(segments, "/golden/") || strings.Contains(segments, "/snapshots/"):
		return basev0.SourceRole_SOURCE_ROLE_FIXTURE, "fixture path convention", .95
	case strings.Contains(segments, "/docs/") || strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".mdx") || strings.HasSuffix(base, ".rst"):
		return basev0.SourceRole_SOURCE_ROLE_DOCS, "documentation path/extension", .95
	case knownSourceConfigName(base) || strings.Contains(segments, "/config/") || strings.Contains(segments, "/configs/"):
		return basev0.SourceRole_SOURCE_ROLE_CONFIG, "configuration path/name convention", .9
	case knownSourceBuildName(base) || strings.Contains(segments, "/build/") || strings.Contains(segments, "/.github/workflows/"):
		return basev0.SourceRole_SOURCE_ROLE_BUILD, "build/CI path/name convention", .9
	case language != basev0.SourceLanguage_SOURCE_LANGUAGE_UNKNOWN:
		return basev0.SourceRole_SOURCE_ROLE_PRODUCTION, "recognized source language outside specialized role", .75
	default:
		return basev0.SourceRole_SOURCE_ROLE_UNKNOWN, "no deterministic role convention matched", .25
	}
}

func knownBinarySourceExtension(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".jar", ".war", ".class", ".so", ".dylib", ".dll", ".exe", ".wasm", ".woff", ".woff2", ".ttf", ".otf", ".mp3", ".mp4", ".mov", ".avi", ".sqlite", ".db":
		return true
	default:
		return false
	}
}

func knownTextSourceName(name string) bool {
	switch strings.ToLower(path.Base(name)) {
	case "dockerfile", "makefile", "procfile", "license", "notice", ".gitignore", ".dockerignore", ".editorconfig":
		return true
	default:
		return false
	}
}

func knownSourceConfigName(base string) bool {
	return strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".config.js") || strings.HasSuffix(base, ".config.ts") ||
		sourceNameIn(base, "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "pyproject.toml", "poetry.lock", "requirements.txt", "cargo.toml", "cargo.lock", "tsconfig.json")
}

func knownSourceBuildName(base string) bool {
	return sourceNameIn(base, "makefile", "dockerfile", "justfile", "taskfile.yml", "taskfile.yaml", "build.gradle", "pom.xml", "workspace", "build", "meson.build", "cmakelists.txt")
}

func sourceNameIn(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
