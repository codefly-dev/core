package cell

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"

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
	// namePattern is one lowercase dotted or dashed segment: a namespace, a
	// module, a service, an account, an endpoint, a binding.
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	// qualifiedServicePattern is a module-qualified service: <module>/<service>.
	qualifiedServicePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*/[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	// objectNamePattern is a Kubernetes object name.
	objectNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	// imageDigestPattern is an OCI manifest digest as a reference carries it.
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// artifactDigestPattern is the SHA-256 of a rendered unit's bytes, hex.
	artifactDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// trustDomainPattern is a SPIFFE trust domain.
	trustDomainPattern = regexp.MustCompile(`^[a-z0-9._-]+$`)
	// hostNamePattern is a host coordinate, component or ownership domain.
	hostNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
)

var workloadKinds = []string{KindDeployment, KindStatefulSet, KindDaemonSet, KindJob, KindCronJob}

// Parse decodes and validates one cell file. Decoding is strict: an unknown
// field is an error rather than a silently ignored intention, which is what
// refuses a cell carrying something no reader polices. The schema is checked
// first and leniently, so a cell of another version is reported as a version
// skew rather than as malformed.
func Parse(data []byte) (*File, error) {
	var header struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if header.Schema != SchemaV1 {
		return nil, fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, header.Schema, SchemaV1)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file File
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the file holds more than one document", ErrInvalid)
	}
	if err := file.Validate(); err != nil {
		return nil, err
	}
	return &file, nil
}

// Validate checks everything a cell can be checked against on its own: the
// host header is whole or absent, the namespaces are one per module in name
// order, every workload names what the platform compares, every identity is
// issued under the cell's trust domain, and every edge names an endpoint the
// cell carries.
func (file *File) Validate() error {
	if file == nil {
		return fmt.Errorf("%w: cell is required", ErrInvalid)
	}
	if file.Schema != SchemaV1 {
		return fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, file.Schema, SchemaV1)
	}
	if !namePattern.MatchString(file.Environment) {
		return fmt.Errorf("%w: environment %q is not a lowercase name", ErrInvalid, file.Environment)
	}
	if err := file.validateHost(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(file.Namespaces))
	modules := make(map[string]struct{}, len(file.Namespaces))
	for index := range file.Namespaces {
		namespace := &file.Namespaces[index]
		if !namePattern.MatchString(namespace.Name) {
			return fmt.Errorf("%w: namespace %q is not a lowercase name", ErrInvalid, namespace.Name)
		}
		if !namePattern.MatchString(namespace.Module) {
			return fmt.Errorf("%w: namespace %s module %q is not a lowercase name", ErrInvalid, namespace.Name, namespace.Module)
		}
		if _, exists := seen[namespace.Name]; exists {
			return fmt.Errorf("%w: namespace %q is declared twice", ErrInvalid, namespace.Name)
		}
		if _, exists := modules[namespace.Module]; exists {
			return fmt.Errorf("%w: module %q is declared in two namespaces; a module renders into one", ErrInvalid, namespace.Module)
		}
		seen[namespace.Name], modules[namespace.Module] = struct{}{}, struct{}{}
		if index > 0 && file.Namespaces[index-1].Name > namespace.Name {
			return fmt.Errorf("%w: namespaces are not in name order (%q after %q); a cell is written deterministically", ErrInvalid, namespace.Name, file.Namespaces[index-1].Name)
		}
		if err := file.validateNamespace(namespace); err != nil {
			return err
		}
	}
	return nil
}

// validateHost holds the host header to all-or-nothing: a cell for a hosted
// environment carries every field, one for an environment with no host none.
func (file *File) validateHost() error {
	fields := []struct{ label, value string }{
		{"coordinate", file.Coordinate}, {"component", file.Component}, {"domain", file.Domain}, {"trust_domain", file.TrustDomain},
	}
	present := 0
	for _, field := range fields {
		if field.value != "" {
			present++
		}
	}
	if present == 0 {
		return nil
	}
	if present != len(fields) {
		return fmt.Errorf("%w: the host header is partial; coordinate, component, domain and trust_domain are carried together or not at all", ErrInvalid)
	}
	for _, field := range fields[:3] {
		if !hostNamePattern.MatchString(field.value) {
			return fmt.Errorf("%w: host %s %q is not a lowercase dotted or slashed name", ErrInvalid, field.label, field.value)
		}
	}
	if !trustDomainPattern.MatchString(file.TrustDomain) {
		return fmt.Errorf("%w: trust_domain %q is not a SPIFFE trust domain", ErrInvalid, file.TrustDomain)
	}
	return nil
}

func (file *File) validateNamespace(namespace *Namespace) error {
	names := make(map[string]struct{}, len(namespace.Workloads))
	for index := range namespace.Workloads {
		workload := &namespace.Workloads[index]
		key := workload.Kind + "/" + workload.Name
		if _, exists := names[key]; exists {
			return fmt.Errorf("%w: namespace %s declares workload %s %q twice", ErrInvalid, namespace.Name, workload.Kind, workload.Name)
		}
		names[key] = struct{}{}
		if index > 0 && namespace.Workloads[index-1].Name > workload.Name {
			return fmt.Errorf("%w: namespace %s workloads are not in name order (%q after %q)", ErrInvalid, namespace.Name, workload.Name, namespace.Workloads[index-1].Name)
		}
		if err := file.validateWorkload(namespace, workload); err != nil {
			return err
		}
	}
	for index := range namespace.Egress {
		egress := &namespace.Egress[index]
		if err := validateEgress(namespace.Name, egress); err != nil {
			return err
		}
		if index > 0 && namespace.Egress[index-1].Service >= egress.Service {
			return fmt.Errorf("%w: namespace %s egress is not in service order, or names a service twice (%q after %q)", ErrInvalid, namespace.Name, egress.Service, namespace.Egress[index-1].Service)
		}
	}
	if namespace.Delivery != nil {
		if err := file.validateDelivery(namespace); err != nil {
			return err
		}
	}
	return nil
}

func (file *File) validateWorkload(namespace *Namespace, workload *Workload) error {
	label := fmt.Sprintf("namespace %s workload %s", namespace.Name, workload.Name)
	if !objectNamePattern.MatchString(workload.Name) {
		return fmt.Errorf("%w: namespace %s workload name %q is not an object name", ErrInvalid, namespace.Name, workload.Name)
	}
	if !slices.Contains(workloadKinds, workload.Kind) {
		return fmt.Errorf("%w: %s kind %q is not one of %s", ErrInvalid, label, workload.Kind, strings.Join(workloadKinds, ", "))
	}
	if err := validateSelector(label, workload.Selector); err != nil {
		return err
	}
	if !qualifiedServicePattern.MatchString(workload.Service) {
		return fmt.Errorf("%w: %s service %q is not <module>/<service>", ErrInvalid, label, workload.Service)
	}
	if module, _, _ := strings.Cut(workload.Service, "/"); module != namespace.Module {
		return fmt.Errorf("%w: %s runs service %q of another module than the namespace's %q", ErrInvalid, label, workload.Service, namespace.Module)
	}
	if !namePattern.MatchString(workload.ServiceAccount) {
		return fmt.Errorf("%w: %s service_account %q is not a lowercase name", ErrInvalid, label, workload.ServiceAccount)
	}
	if err := file.validateSPIFFEID(label, namespace.Name, workload.ServiceAccount, workload.SPIFFEID); err != nil {
		return err
	}
	if err := validateContainers(label, workload); err != nil {
		return err
	}
	if !namePattern.MatchString(workload.Artifact.Name) || !artifactDigestPattern.MatchString(workload.Artifact.Digest) {
		return fmt.Errorf("%w: %s artifact must name the rendered unit and the hex SHA-256 of its rendered bytes, got %q at %q", ErrInvalid, label, workload.Artifact.Name, workload.Artifact.Digest)
	}
	if release := workload.Release; release != nil && (release.Publisher == "" || release.Name == "" || release.Version == "") {
		return fmt.Errorf("%w: %s release must carry publisher, name and version", ErrInvalid, label)
	}
	if err := validateEndpoints(label, workload); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(workload.Bindings))
	for _, binding := range workload.Bindings {
		if !namePattern.MatchString(binding) {
			return fmt.Errorf("%w: %s binding %q is not a lowercase name", ErrInvalid, label, binding)
		}
		if _, exists := seen[binding]; exists {
			return fmt.Errorf("%w: %s binding %q is declared twice", ErrInvalid, label, binding)
		}
		seen[binding] = struct{}{}
	}
	return nil
}

func validateSelector(label string, selector map[string]string) error {
	if len(selector) == 0 {
		return fmt.Errorf("%w: %s carries no selector; the exact label set that selects its pods is what a policy matches", ErrInvalid, label)
	}
	for key, value := range selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || key != strings.TrimSpace(key) {
			return fmt.Errorf("%w: %s selector label %q=%q is empty or padded", ErrInvalid, label, key, value)
		}
	}
	return nil
}

// validateSPIFFEID holds an identity to the cell's trust domain, the
// namespace and the account: the string is what the platform matches, and it
// must say what the fields beside it say.
func (file *File) validateSPIFFEID(label, namespace, account, id string) error {
	if file.TrustDomain == "" {
		if id != "" {
			return fmt.Errorf("%w: %s carries spiffe_id %q but the cell declares no trust domain", ErrInvalid, label, id)
		}
		return nil
	}
	want := "spiffe://" + file.TrustDomain + "/ns/" + namespace + "/sa/" + account
	if id != want {
		return fmt.Errorf("%w: %s spiffe_id is %q, and the trust domain, namespace and account beside it say %q", ErrInvalid, label, id, want)
	}
	return nil
}

func validateContainers(label string, workload *Workload) error {
	if len(workload.Containers) == 0 {
		return fmt.Errorf("%w: %s declares no container", ErrInvalid, label)
	}
	names := make(map[string]struct{}, len(workload.Containers)+len(workload.InitContainers))
	for _, group := range []struct {
		what       string
		containers []Container
	}{{"container", workload.Containers}, {"init container", workload.InitContainers}} {
		for _, container := range group.containers {
			if !objectNamePattern.MatchString(container.Name) {
				return fmt.Errorf("%w: %s %s name %q is not a container name", ErrInvalid, label, group.what, container.Name)
			}
			if _, exists := names[container.Name]; exists {
				return fmt.Errorf("%w: %s names %s %q twice", ErrInvalid, label, group.what, container.Name)
			}
			names[container.Name] = struct{}{}
			if container.Image.Repository == "" || !imageDigestPattern.MatchString(container.Image.Digest) {
				return fmt.Errorf("%w: %s %s %q must pin a repository and an OCI manifest digest (sha256:<64 hex>), got %q at %q", ErrInvalid, label, group.what, container.Name, container.Image.Repository, container.Image.Digest)
			}
		}
	}
	if !slices.ContainsFunc(workload.Containers, func(container Container) bool { return container.Name == workload.Authenticating }) {
		return fmt.Errorf("%w: %s names %q as its authenticating container, which is not one of its containers; the one that authenticates is named, never inferred", ErrInvalid, label, workload.Authenticating)
	}
	return nil
}

func validateEndpoints(label string, workload *Workload) error {
	names := make(map[string]struct{}, len(workload.Endpoints))
	for index, endpoint := range workload.Endpoints {
		if !namePattern.MatchString(endpoint.Name) {
			return fmt.Errorf("%w: %s endpoint %q is not a lowercase name", ErrInvalid, label, endpoint.Name)
		}
		if _, exists := names[endpoint.Name]; exists {
			return fmt.Errorf("%w: %s endpoint %q is declared twice", ErrInvalid, label, endpoint.Name)
		}
		names[endpoint.Name] = struct{}{}
		if index > 0 && workload.Endpoints[index-1].Name > endpoint.Name {
			return fmt.Errorf("%w: %s endpoints are not in name order", ErrInvalid, label)
		}
		if endpoint.Port > 65535 {
			return fmt.Errorf("%w: %s endpoint %s port %d is not a port", ErrInvalid, label, endpoint.Name, endpoint.Port)
		}
		for _, consumer := range endpoint.Consumers {
			if !qualifiedServicePattern.MatchString(consumer) {
				return fmt.Errorf("%w: %s endpoint %s consumer %q is not <module>/<service>", ErrInvalid, label, endpoint.Name, consumer)
			}
		}
		if !sort.StringsAreSorted(endpoint.Consumers) {
			return fmt.Errorf("%w: %s endpoint %s consumers are not in order", ErrInvalid, label, endpoint.Name)
		}
	}
	for _, route := range workload.Ingress {
		if _, exists := names[route.Endpoint]; !exists {
			return fmt.Errorf("%w: %s ingress names endpoint %q, which the workload does not serve", ErrInvalid, label, route.Endpoint)
		}
		if len(route.Hosts) == 0 {
			return fmt.Errorf("%w: %s ingress to %s names no host", ErrInvalid, label, route.Endpoint)
		}
		for _, host := range route.Hosts {
			if strings.TrimSpace(host) == "" || strings.ContainsAny(host, " /") {
				return fmt.Errorf("%w: %s ingress to %s host %q is not a host name", ErrInvalid, label, route.Endpoint, host)
			}
		}
	}
	return nil
}

func validateEgress(namespace string, egress *Egress) error {
	if !qualifiedServicePattern.MatchString(egress.Service) {
		return fmt.Errorf("%w: namespace %s egress service %q is not <module>/<service>", ErrInvalid, namespace, egress.Service)
	}
	if len(egress.Hosts) == 0 && len(egress.CIDRs) == 0 {
		return fmt.Errorf("%w: namespace %s egress for %s declares neither a host nor a CIDR", ErrInvalid, namespace, egress.Service)
	}
	for _, host := range egress.Hosts {
		if strings.TrimSpace(host.Name) == "" || strings.ContainsAny(host.Name, " /:") {
			return fmt.Errorf("%w: namespace %s egress for %s host %q is not a host name", ErrInvalid, namespace, egress.Service, host.Name)
		}
		if host.Port < 1 || host.Port > 65535 {
			return fmt.Errorf("%w: namespace %s egress for %s host %s port %d is not a port; the port is always explicit", ErrInvalid, namespace, egress.Service, host.Name, host.Port)
		}
	}
	for _, cidr := range egress.CIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("%w: namespace %s egress for %s CIDR %q: %v", ErrInvalid, namespace, egress.Service, cidr, err)
		}
	}
	return nil
}

func (file *File) validateDelivery(namespace *Namespace) error {
	label := "namespace " + namespace.Name + " delivery"
	delivery := namespace.Delivery
	if delivery.Kind != KindJob {
		return fmt.Errorf("%w: %s kind %q is not a Job", ErrInvalid, label, delivery.Kind)
	}
	if err := validateSelector(label, delivery.Selector); err != nil {
		return err
	}
	if !namePattern.MatchString(delivery.ServiceAccount) {
		return fmt.Errorf("%w: %s service_account %q is not a lowercase name", ErrInvalid, label, delivery.ServiceAccount)
	}
	if err := file.validateSPIFFEID(label, namespace.Name, delivery.ServiceAccount, delivery.SPIFFEID); err != nil {
		return err
	}
	if !objectNamePattern.MatchString(delivery.Container) {
		return fmt.Errorf("%w: %s container %q is not a container name", ErrInvalid, label, delivery.Container)
	}
	if delivery.Image.Repository == "" || !imageDigestPattern.MatchString(delivery.Image.Digest) {
		return fmt.Errorf("%w: %s must pin a repository and an OCI manifest digest, got %q at %q", ErrInvalid, label, delivery.Image.Repository, delivery.Image.Digest)
	}
	return nil
}
