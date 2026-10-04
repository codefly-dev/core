package cell

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

var (
	// ErrSchema means the file does not declare a schema this package reads: a
	// version skew, not a malformed cell.
	ErrSchema = errors.New("cell schema is not supported")
	// ErrInvalid means the cell violates its own rules.
	ErrInvalid = errors.New("cell is invalid")
)

var (
	// namePattern is one lowercase dotted or dashed segment: a module, a
	// service, an endpoint, a binding, an environment.
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	// qualifiedServicePattern is a module-qualified service: <module>/<service>.
	qualifiedServicePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*/[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	// digestPattern is an OCI manifest digest as a reference carries it, and
	// the SHA-256 of a rendered unit's bytes in the form the render writes it:
	// sha256:<64 hex>.
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// trustDomainPattern is a SPIFFE trust domain.
	trustDomainPattern = regexp.MustCompile(`^[a-z0-9._-]+$`)
	// hostNamePattern is a host coordinate, component or ownership domain.
	hostNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
)

// The Kubernetes name and label grammars, as k8s.io/apimachinery/pkg/util/
// validation states them (IsDNS1123Label, IsDNS1123Subdomain, IsQualifiedName,
// IsValidLabelValue), spelled here so this package holds a cell to EXACTLY
// what the API server would accept — an empty label value is legal and an
// uppercase namespace is not — without importing the API machinery.
const (
	dns1123LabelFmt     = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
	dns1123SubdomainFmt = dns1123LabelFmt + "(\\." + dns1123LabelFmt + ")*"
	qualifiedNameFmt    = "([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]"

	dns1123LabelMaxLength     = 63
	dns1123SubdomainMaxLength = 253
	qualifiedNameMaxLength    = 63
	labelValueMaxLength       = 63
)

var (
	dns1123LabelPattern     = regexp.MustCompile("^" + dns1123LabelFmt + "$")
	dns1123SubdomainPattern = regexp.MustCompile("^" + dns1123SubdomainFmt + "$")
	qualifiedNamePattern    = regexp.MustCompile("^" + qualifiedNameFmt + "$")
	labelValuePattern       = regexp.MustCompile("^(" + qualifiedNameFmt + ")?$")
)

var workloadKinds = []string{KindDeployment, KindStatefulSet, KindDaemonSet, KindJob, KindCronJob}

// Parse decodes and validates one cell file. Decoding is strict: an unknown
// field is an error rather than a silently ignored intention, which is what
// refuses a cell carrying something no reader polices. The schema is checked
// first and leniently, so a cell of another version is reported as a version
// skew rather than as malformed.
func Parse(data []byte) (*File, error) { return parse(data, "") }

// parse is Parse with one rule deleted — the self-check's entrypoint, which
// proves every rule is protected by a fixture. "" deletes none.
func parse(data []byte, without string) (*File, error) {
	var header struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if header.Schema != SchemaV1 && without != ruleSchema {
		return nil, fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, header.Schema, SchemaV1)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(without != ruleKnownFields)
	var file File
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) && without != ruleOneDocument {
		return nil, fmt.Errorf("%w: the file holds more than one document", ErrInvalid)
	}
	if err := file.validate(without); err != nil {
		return nil, err
	}
	return &file, nil
}

// Validate checks everything a cell can be checked against on its own: every
// rule of rules.go, in order, the first refusal named — the host header is
// whole or absent, the namespaces are one per module in name order, every
// workload names what the platform compares in the exact grammar Kubernetes
// accepts, every identity is issued under the cell's trust domain, every
// image pins a canonical repository and a digest, every edge names an endpoint
// the cell carries, and every egress belongs to the namespace's module.
func (file *File) Validate() error { return file.validate("") }
