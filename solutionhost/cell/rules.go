package cell

import (
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"

	"github.com/distribution/reference"

	"github.com/codefly-dev/core/resources/names"
)

// A rule is one refusal this reader enforces, named: a conformance fixture
// says which rule refuses it, and the kit's self-check deletes each rule in
// turn and proves at least one fixture notices. Validate runs every rule in
// this order and returns the first refusal, so a cell is refused for one
// reason, named — the same reason whichever reader refused it.
//
// Three refusals are the decoder's (ruleSchema, ruleKnownFields,
// ruleOneDocument) and one is inherent (ruleWellFormed: a document that is
// not YAML has nothing to validate); they are listed here so the fixture
// table and the self-check cover them like any other.
type rule struct {
	name  string
	check func(file *File) error
	// inherent marks a rule the self-check cannot delete: YAML syntax is not
	// a rule of this package, only a precondition of reading anything.
	inherent bool
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	ruleWellFormed  = "well-formed"
	ruleSchema      = "schema"
	ruleKnownFields = "known-fields"
	ruleOneDocument = "one-document"

	ruleEnvironmentName = "environment-name"
	ruleHostHeaderWhole = "host-header-whole"
	ruleHostName        = "host-name"
	ruleTrustDomain     = "trust-domain"

	ruleNamespaceName    = "namespace-name"
	ruleNamespaceModule  = "namespace-module"
	ruleNamespaceUnique  = "namespace-unique"
	ruleModuleOnce       = "module-once"
	ruleNamespacesOrder  = "namespaces-ordered"
	ruleWorkloadUnique   = "workload-unique"
	ruleWorkloadsOrder   = "workloads-ordered"
	ruleWorkloadName     = "workload-name"
	ruleWorkloadKind     = "workload-kind"
	ruleSelectorPresent  = "selector-present"
	ruleSelectorLabelKey = "selector-label-key"
	ruleSelectorLabelVal = "selector-label-value"
	ruleServiceQualified = "service-qualified"
	ruleServiceOfModule  = "service-of-module"
	ruleAccountName      = "account-name"
	ruleSPIFFEIDHostless = "spiffe-id-hostless"
	ruleSPIFFEID         = "spiffe-id"
	ruleContainersExist  = "containers-present"
	ruleContainerName    = "container-name"
	ruleContainerUnique  = "container-unique"
	ruleImageRepository  = "image-repository"
	ruleImageDigest      = "image-digest"
	ruleAuthenticating   = "authenticating-named"
	ruleArtifact         = "artifact"
	ruleReleaseWhole     = "release-whole"
	ruleEndpointName     = "endpoint-name"
	ruleEndpointUnique   = "endpoint-unique"
	ruleEndpointsOrder   = "endpoints-ordered"
	ruleEndpointPort     = "endpoint-port"
	ruleVisibility       = "endpoint-visibility"
	ruleAllowModules     = "allow-modules"
	ruleAllowNeedsInner  = "allow-modules-need-internal"
	ruleConsumerQualify  = "consumer-qualified"
	ruleConsumersOrder   = "consumers-ordered"
	ruleIngressServed    = "ingress-served"
	ruleIngressHasHost   = "ingress-has-host"
	ruleIngressHostName  = "ingress-host-name"
	ruleIngressOrder     = "ingress-ordered"
	ruleBindingName      = "binding-name"
	ruleBindingUnique    = "binding-unique"

	ruleEgressQualified = "egress-service-qualified"
	ruleEgressOfModule  = "egress-service-of-module"
	ruleEgressHasTarget = "egress-has-target"
	ruleEgressHostName  = "egress-host-name"
	ruleEgressHostPort  = "egress-host-port"
	ruleEgressCIDR      = "egress-cidr"
	ruleEgressOrder     = "egress-ordered"

	ruleDeliveryKind      = "delivery-kind"
	ruleDeliveryAccount   = "delivery-account"
	ruleDeliveryContainer = "delivery-container-name"
)

// rules is the order a cell is held to: the document's shape first, then the
// header, then each namespace's entries in the order the file is written —
// what every workload and the delivery Job share (selectors, identities,
// images) as one rule each, applied over both.
func rules() []rule {
	return []rule{
		{name: ruleWellFormed, inherent: true, check: func(*File) error { return nil }},
		{name: ruleSchema, check: checkSchema},
		{name: ruleKnownFields, check: func(*File) error { return nil }},
		{name: ruleOneDocument, check: func(*File) error { return nil }},
		{name: ruleEnvironmentName, check: checkEnvironmentName},
		{name: ruleHostHeaderWhole, check: checkHostHeaderWhole},
		{name: ruleHostName, check: checkHostNames},
		{name: ruleTrustDomain, check: checkTrustDomain},
		{name: ruleNamespaceName, check: checkNamespaceNames},
		{name: ruleNamespaceModule, check: checkNamespaceModules},
		{name: ruleNamespaceUnique, check: checkNamespacesUnique},
		{name: ruleModuleOnce, check: checkModulesOnce},
		{name: ruleNamespacesOrder, check: checkNamespacesOrdered},
		{name: ruleWorkloadUnique, check: checkWorkloadsUnique},
		{name: ruleWorkloadsOrder, check: checkWorkloadsOrdered},
		{name: ruleWorkloadName, check: checkWorkloadNames},
		{name: ruleWorkloadKind, check: checkWorkloadKinds},
		{name: ruleSelectorPresent, check: checkSelectorsPresent},
		{name: ruleSelectorLabelKey, check: checkSelectorLabelKeys},
		{name: ruleSelectorLabelVal, check: checkSelectorLabelValues},
		{name: ruleServiceQualified, check: checkServicesQualified},
		{name: ruleServiceOfModule, check: checkServicesOfModule},
		{name: ruleAccountName, check: checkAccountNames},
		{name: ruleSPIFFEIDHostless, check: checkSPIFFEIDsHostless},
		{name: ruleSPIFFEID, check: checkSPIFFEIDs},
		{name: ruleContainersExist, check: checkContainersPresent},
		{name: ruleContainerName, check: checkContainerNames},
		{name: ruleContainerUnique, check: checkContainersUnique},
		{name: ruleImageRepository, check: checkImageRepositories},
		{name: ruleImageDigest, check: checkImageDigests},
		{name: ruleAuthenticating, check: checkAuthenticatingNamed},
		{name: ruleArtifact, check: checkArtifacts},
		{name: ruleReleaseWhole, check: checkReleasesWhole},
		{name: ruleEndpointName, check: checkEndpointNames},
		{name: ruleEndpointUnique, check: checkEndpointsUnique},
		{name: ruleEndpointsOrder, check: checkEndpointsOrdered},
		{name: ruleEndpointPort, check: checkEndpointPorts},
		{name: ruleVisibility, check: checkVisibilities},
		{name: ruleAllowModules, check: checkAllowModules},
		{name: ruleAllowNeedsInner, check: checkAllowModulesNeedInternal},
		{name: ruleConsumerQualify, check: checkConsumersQualified},
		{name: ruleConsumersOrder, check: checkConsumersOrdered},
		{name: ruleIngressServed, check: checkIngressServed},
		{name: ruleIngressHasHost, check: checkIngressHasHost},
		{name: ruleIngressHostName, check: checkIngressHostNames},
		{name: ruleIngressOrder, check: checkIngressOrdered},
		{name: ruleBindingName, check: checkBindingNames},
		{name: ruleBindingUnique, check: checkBindingsUnique},
		{name: ruleEgressQualified, check: checkEgressQualified},
		{name: ruleEgressOfModule, check: checkEgressOfModule},
		{name: ruleEgressHasTarget, check: checkEgressHasTarget},
		{name: ruleEgressHostName, check: checkEgressHostNames},
		{name: ruleEgressHostPort, check: checkEgressHostPorts},
		{name: ruleEgressCIDR, check: checkEgressCIDRs},
		{name: ruleEgressOrder, check: checkEgressOrdered},
		{name: ruleDeliveryKind, check: checkDeliveryKinds},
		{name: ruleDeliveryAccount, check: checkDeliveryAccounts},
		{name: ruleDeliveryContainer, check: checkDeliveryContainerNames},
	}
}

// ruleNames lists every rule, in order.
func ruleNames() []string {
	all := rules()
	names := make([]string, 0, len(all))
	for _, r := range all {
		names = append(names, r.name)
	}
	return names
}

// validate runs every rule but the one named, in order; "" deletes none.
func (file *File) validate(without string) error {
	if file == nil {
		return fmt.Errorf("%w: cell is required", ErrInvalid)
	}
	for _, r := range rules() {
		if r.name == without {
			continue
		}
		if err := r.check(file); err != nil {
			return err
		}
	}
	return nil
}

// What a refusal calls the container it names, and why a repository carrying
// more than a name is refused.
const (
	whatContainer     = "container"
	whatInitContainer = "init container"
	reasonTagOrDigest = "it carries a tag or a digest"
)

// --- the document and its header ---------------------------------------

func checkSchema(file *File) error {
	if file.Schema != SchemaV1 {
		return fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, file.Schema, SchemaV1)
	}
	return nil
}

func checkEnvironmentName(file *File) error {
	if !namePattern.MatchString(file.Environment) {
		return fmt.Errorf("%w: environment %q is not a lowercase name", ErrInvalid, file.Environment)
	}
	return nil
}

// hostFields are the header's four fields, carried together or not at all.
func (file *File) hostFields() []struct{ label, value string } {
	return []struct{ label, value string }{
		{"coordinate", file.Coordinate}, {"component", file.Component}, {"domain", file.Domain}, {"trust_domain", file.TrustDomain},
	}
}

// hosted reports a cell carrying a host header.
func (file *File) hosted() bool {
	for _, field := range file.hostFields() {
		if field.value != "" {
			return true
		}
	}
	return false
}

func checkHostHeaderWhole(file *File) error {
	present := 0
	for _, field := range file.hostFields() {
		if field.value != "" {
			present++
		}
	}
	if present != 0 && present != len(file.hostFields()) {
		return fmt.Errorf("%w: the host header is partial; coordinate, component, domain and trust_domain are carried together or not at all", ErrInvalid)
	}
	return nil
}

func checkHostNames(file *File) error {
	if !file.hosted() {
		return nil
	}
	for _, field := range file.hostFields()[:3] {
		if !hostNamePattern.MatchString(field.value) {
			return fmt.Errorf("%w: host %s %q is not a lowercase dotted or slashed name", ErrInvalid, field.label, field.value)
		}
	}
	return nil
}

func checkTrustDomain(file *File) error {
	if file.hosted() && !trustDomainPattern.MatchString(file.TrustDomain) {
		return fmt.Errorf("%w: trust_domain %q is not a SPIFFE trust domain", ErrInvalid, file.TrustDomain)
	}
	return nil
}

// --- namespaces ----------------------------------------------------------

func checkNamespaceNames(file *File) error {
	for _, namespace := range file.Namespaces {
		if !isDNS1123Label(namespace.Name) {
			return fmt.Errorf("%w: namespace %q is not a DNS label (lowercase alphanumerics and dashes, at most %d characters)", ErrInvalid, namespace.Name, dns1123LabelMaxLength)
		}
	}
	return nil
}

func checkNamespaceModules(file *File) error {
	for _, namespace := range file.Namespaces {
		if !namePattern.MatchString(namespace.Module) {
			return fmt.Errorf("%w: namespace %s module %q is not a lowercase name", ErrInvalid, namespace.Name, namespace.Module)
		}
	}
	return nil
}

func checkNamespacesUnique(file *File) error {
	names := make([]string, 0, len(file.Namespaces))
	for _, namespace := range file.Namespaces {
		names = append(names, namespace.Name)
	}
	if duplicate, found := firstDuplicate(names); found {
		return fmt.Errorf("%w: namespace %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

func checkModulesOnce(file *File) error {
	modules := make([]string, 0, len(file.Namespaces))
	for _, namespace := range file.Namespaces {
		modules = append(modules, namespace.Module)
	}
	if duplicate, found := firstDuplicate(modules); found {
		return fmt.Errorf("%w: module %q is declared in two namespaces; a module renders into one", ErrInvalid, duplicate)
	}
	return nil
}

func checkNamespacesOrdered(file *File) error {
	for index := 1; index < len(file.Namespaces); index++ {
		if file.Namespaces[index-1].Name > file.Namespaces[index].Name {
			return fmt.Errorf("%w: namespaces are not in name order (%q after %q); a cell is written deterministically", ErrInvalid, file.Namespaces[index].Name, file.Namespaces[index-1].Name)
		}
	}
	return nil
}

// firstDuplicate reports the first value that recurs, in declaration order.
func firstDuplicate(values []string) (string, bool) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return value, true
		}
		seen[value] = struct{}{}
	}
	return "", false
}

// --- workloads -----------------------------------------------------------

// labelledWorkload is one workload with its namespace and the label a
// refusal names it by.
type labelledWorkload struct {
	label     string
	namespace *Namespace
	workload  *Workload
}

// workloads lists every workload of the cell, in file order.
func (file *File) workloads() []labelledWorkload {
	var all []labelledWorkload
	for n := range file.Namespaces {
		namespace := &file.Namespaces[n]
		for w := range namespace.Workloads {
			workload := &namespace.Workloads[w]
			all = append(all, labelledWorkload{fmt.Sprintf("namespace %s workload %s", namespace.Name, workload.Name), namespace, workload})
		}
	}
	return all
}

func checkWorkloadsUnique(file *File) error {
	for n := range file.Namespaces {
		namespace := &file.Namespaces[n]
		keys := make([]string, 0, len(namespace.Workloads))
		for _, workload := range namespace.Workloads {
			keys = append(keys, workload.Kind+"/"+workload.Name)
		}
		if duplicate, found := firstDuplicate(keys); found {
			kind, name, _ := strings.Cut(duplicate, "/")
			return fmt.Errorf("%w: namespace %s declares workload %s %q twice", ErrInvalid, namespace.Name, kind, name)
		}
	}
	return nil
}

func checkWorkloadsOrdered(file *File) error {
	for _, namespace := range file.Namespaces {
		for index := 1; index < len(namespace.Workloads); index++ {
			if namespace.Workloads[index-1].Name > namespace.Workloads[index].Name {
				return fmt.Errorf("%w: namespace %s workloads are not in name order (%q after %q)", ErrInvalid, namespace.Name, namespace.Workloads[index].Name, namespace.Workloads[index-1].Name)
			}
		}
	}
	return nil
}

func checkWorkloadNames(file *File) error {
	for _, entry := range file.workloads() {
		if !isDNS1123Subdomain(entry.workload.Name) {
			return fmt.Errorf("%w: namespace %s workload name %q is not a DNS subdomain (lowercase alphanumerics, dashes and dots, at most %d characters)", ErrInvalid, entry.namespace.Name, entry.workload.Name, dns1123SubdomainMaxLength)
		}
	}
	return nil
}

func checkWorkloadKinds(file *File) error {
	for _, entry := range file.workloads() {
		if !slices.Contains(workloadKinds, entry.workload.Kind) {
			return fmt.Errorf("%w: %s kind %q is not one of %s", ErrInvalid, entry.label, entry.workload.Kind, strings.Join(workloadKinds, ", "))
		}
	}
	return nil
}

// labelledSelector is one selector — a workload's or a delivery Job's — with
// the label a refusal names it by.
type labelledSelector struct {
	label    string
	selector map[string]string
}

// selectors lists every selector the cell carries: each workload's, then
// each namespace's delivery Job's.
func (file *File) selectors() []labelledSelector {
	var all []labelledSelector
	for _, entry := range file.workloads() {
		all = append(all, labelledSelector{entry.label, entry.workload.Selector})
	}
	for _, namespace := range file.Namespaces {
		if namespace.Delivery != nil {
			all = append(all, labelledSelector{"namespace " + namespace.Name + " delivery", namespace.Delivery.Selector})
		}
	}
	return all
}

func checkSelectorsPresent(file *File) error {
	for _, entry := range file.selectors() {
		if len(entry.selector) == 0 {
			return fmt.Errorf("%w: %s carries no selector; the exact label set that selects its pods is what a policy matches", ErrInvalid, entry.label)
		}
	}
	return nil
}

// sortedKeys lists a selector's keys in order, so a refusal names the same
// label on every run.
func sortedKeys(selector map[string]string) []string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func checkSelectorLabelKeys(file *File) error {
	for _, entry := range file.selectors() {
		for _, key := range sortedKeys(entry.selector) {
			if !isQualifiedName(key) {
				return fmt.Errorf("%w: %s selector label key %q is not a Kubernetes label key (an optional DNS-subdomain prefix and a slash, then up to %d alphanumerics, dashes, underscores or dots beginning and ending alphanumeric)", ErrInvalid, entry.label, key, qualifiedNameMaxLength)
			}
		}
	}
	return nil
}

func checkSelectorLabelValues(file *File) error {
	for _, entry := range file.selectors() {
		for _, key := range sortedKeys(entry.selector) {
			if value := entry.selector[key]; !isLabelValue(value) {
				return fmt.Errorf("%w: %s selector label %s=%q is not a Kubernetes label value (empty, or up to %d alphanumerics, dashes, underscores or dots beginning and ending alphanumeric)", ErrInvalid, entry.label, key, value, labelValueMaxLength)
			}
		}
	}
	return nil
}

// isDNS1123Label, isDNS1123Subdomain, isQualifiedName and isLabelValue are
// Kubernetes' own grammars (apimachinery's validation package), held to the
// same lengths.
func isDNS1123Label(value string) bool {
	return len(value) <= dns1123LabelMaxLength && dns1123LabelPattern.MatchString(value)
}

func isDNS1123Subdomain(value string) bool {
	return len(value) <= dns1123SubdomainMaxLength && dns1123SubdomainPattern.MatchString(value)
}

func isQualifiedName(value string) bool {
	parts := strings.Split(value, "/")
	var name string
	switch len(parts) {
	case 1:
		name = parts[0]
	case 2:
		prefix, part := parts[0], parts[1]
		if prefix == "" || !isDNS1123Subdomain(prefix) {
			return false
		}
		name = part
	default:
		return false
	}
	return name != "" && len(name) <= qualifiedNameMaxLength && qualifiedNamePattern.MatchString(name)
}

func isLabelValue(value string) bool {
	return len(value) <= labelValueMaxLength && labelValuePattern.MatchString(value)
}

func checkServicesQualified(file *File) error {
	for _, entry := range file.workloads() {
		if !qualifiedServicePattern.MatchString(entry.workload.Service) {
			return fmt.Errorf("%w: %s service %q is not <module>/<service>", ErrInvalid, entry.label, entry.workload.Service)
		}
	}
	return nil
}

func checkServicesOfModule(file *File) error {
	for _, entry := range file.workloads() {
		if module, _, _ := strings.Cut(entry.workload.Service, "/"); module != entry.namespace.Module {
			return fmt.Errorf("%w: %s runs service %q of another module than the namespace's %q", ErrInvalid, entry.label, entry.workload.Service, entry.namespace.Module)
		}
	}
	return nil
}

func checkAccountNames(file *File) error {
	for _, entry := range file.workloads() {
		if !isDNS1123Subdomain(entry.workload.ServiceAccount) {
			return fmt.Errorf("%w: %s service_account %q is not a DNS subdomain (lowercase alphanumerics, dashes and dots, at most %d characters)", ErrInvalid, entry.label, entry.workload.ServiceAccount, dns1123SubdomainMaxLength)
		}
	}
	return nil
}

// labelledIdentity is one identity the cell carries — a workload's or a
// delivery Job's — with the namespace and account it must be issued for.
type labelledIdentity struct {
	label     string
	namespace string
	account   string
	id        string
}

// identities lists every identity the cell carries: each workload's, then
// each namespace's delivery Job's.
func (file *File) identities() []labelledIdentity {
	var all []labelledIdentity
	for _, entry := range file.workloads() {
		all = append(all, labelledIdentity{entry.label, entry.namespace.Name, entry.workload.ServiceAccount, entry.workload.SPIFFEID})
	}
	for _, namespace := range file.Namespaces {
		if namespace.Delivery != nil {
			all = append(all, labelledIdentity{"namespace " + namespace.Name + " delivery", namespace.Name, namespace.Delivery.ServiceAccount, namespace.Delivery.SPIFFEID})
		}
	}
	return all
}

func checkSPIFFEIDsHostless(file *File) error {
	if file.TrustDomain != "" {
		return nil
	}
	for _, entry := range file.identities() {
		if entry.id != "" {
			return fmt.Errorf("%w: %s carries spiffe_id %q but the cell declares no trust domain", ErrInvalid, entry.label, entry.id)
		}
	}
	return nil
}

// checkSPIFFEIDs holds an identity to the cell's trust domain, the namespace
// and the account: the string is what the platform matches, and it must say
// what the fields beside it say.
func checkSPIFFEIDs(file *File) error {
	if file.TrustDomain == "" {
		return nil
	}
	for _, entry := range file.identities() {
		want := "spiffe://" + file.TrustDomain + "/ns/" + entry.namespace + "/sa/" + entry.account
		if entry.id != want {
			return fmt.Errorf("%w: %s spiffe_id is %q, and the trust domain, namespace and account beside it say %q", ErrInvalid, entry.label, entry.id, want)
		}
	}
	return nil
}

func checkContainersPresent(file *File) error {
	for _, entry := range file.workloads() {
		if len(entry.workload.Containers) == 0 {
			return fmt.Errorf("%w: %s declares no container", ErrInvalid, entry.label)
		}
	}
	return nil
}

// labelledContainer is one container — of a workload or a delivery Job —
// with the label a refusal names it by and what it is called.
type labelledContainer struct {
	label string
	what  string
	name  string
	image Image
}

// containers lists every container of a workload, init containers included,
// in file order.
func (entry labelledWorkload) containers() []labelledContainer {
	var all []labelledContainer
	for _, container := range entry.workload.Containers {
		all = append(all, labelledContainer{entry.label, whatContainer, container.Name, container.Image})
	}
	for _, container := range entry.workload.InitContainers {
		all = append(all, labelledContainer{entry.label, whatInitContainer, container.Name, container.Image})
	}
	return all
}

// images lists every image the cell pins: every container of every workload,
// then each namespace's delivery Job's.
func (file *File) images() []labelledContainer {
	var all []labelledContainer
	for _, entry := range file.workloads() {
		all = append(all, entry.containers()...)
	}
	for _, namespace := range file.Namespaces {
		if namespace.Delivery != nil {
			all = append(all, labelledContainer{"namespace " + namespace.Name + " delivery", whatContainer, namespace.Delivery.Container, namespace.Delivery.Image})
		}
	}
	return all
}

func checkContainerNames(file *File) error {
	for _, entry := range file.workloads() {
		for _, container := range entry.containers() {
			if !isDNS1123Label(container.name) {
				return fmt.Errorf("%w: %s %s name %q is not a DNS label (lowercase alphanumerics and dashes, at most %d characters)", ErrInvalid, entry.label, container.what, container.name, dns1123LabelMaxLength)
			}
		}
	}
	return nil
}

func checkContainersUnique(file *File) error {
	for _, entry := range file.workloads() {
		containers := entry.containers()
		names := make([]string, 0, len(containers))
		for _, container := range containers {
			names = append(names, container.name)
		}
		if duplicate, found := firstDuplicate(names); found {
			what := whatContainer
			for _, container := range containers {
				if container.name == duplicate {
					what = container.what
				}
			}
			return fmt.Errorf("%w: %s names %s %q twice", ErrInvalid, entry.label, what, duplicate)
		}
	}
	return nil
}

// checkImageRepositories holds every repository to the canonical image
// repository grammar — registry and path, no tag, no digest — through the
// distribution reference parser: the repository and the digest beside it are
// the two things the platform compares, so a repository carrying either, or
// not parsing at all, is refused rather than compared loosely.
func checkImageRepositories(file *File) error {
	for _, entry := range file.images() {
		if reason := repositoryDefect(entry.image.Repository); reason != "" {
			return fmt.Errorf("%w: %s %s %q image repository %q is not a canonical image repository (registry/path, no tag, no digest; the digest is carried beside it): %s", ErrInvalid, entry.label, entry.what, entry.name, entry.image.Repository, reason)
		}
	}
	return nil
}

// repositoryDefect reports why a repository string is not a canonical
// repository, or "" when it is one.
func repositoryDefect(repository string) string {
	named, err := reference.ParseNamed(repository)
	if err != nil {
		return err.Error()
	}
	if !reference.IsNameOnly(named) {
		return reasonTagOrDigest
	}
	if named.String() != repository {
		return fmt.Sprintf("it reads back as %q", named.String())
	}
	return ""
}

func checkImageDigests(file *File) error {
	for _, entry := range file.images() {
		if !digestPattern.MatchString(entry.image.Digest) {
			return fmt.Errorf("%w: %s %s %q must pin an OCI manifest digest (sha256:<64 hex>), got %q", ErrInvalid, entry.label, entry.what, entry.name, entry.image.Digest)
		}
	}
	return nil
}

func checkAuthenticatingNamed(file *File) error {
	for _, entry := range file.workloads() {
		workload := entry.workload
		if !slices.ContainsFunc(workload.Containers, func(container Container) bool { return container.Name == workload.Authenticating }) {
			return fmt.Errorf("%w: %s names %q as its authenticating container, which is not one of its containers; the one that authenticates is named, never inferred", ErrInvalid, entry.label, workload.Authenticating)
		}
	}
	return nil
}

func checkArtifacts(file *File) error {
	for _, entry := range file.workloads() {
		artifact := entry.workload.Artifact
		if !namePattern.MatchString(artifact.Name) || !digestPattern.MatchString(artifact.Digest) {
			return fmt.Errorf("%w: %s artifact must name the rendered unit and the SHA-256 of its rendered bytes (sha256:<64 hex>), got %q at %q", ErrInvalid, entry.label, artifact.Name, artifact.Digest)
		}
	}
	return nil
}

func checkReleasesWhole(file *File) error {
	for _, entry := range file.workloads() {
		if release := entry.workload.Release; release != nil && (release.Publisher == "" || release.Name == "" || release.Version == "") {
			return fmt.Errorf("%w: %s release must carry publisher, name and version", ErrInvalid, entry.label)
		}
	}
	return nil
}

func checkEndpointNames(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			if !namePattern.MatchString(endpoint.Name) {
				return fmt.Errorf("%w: %s endpoint %q is not a lowercase name", ErrInvalid, entry.label, endpoint.Name)
			}
		}
	}
	return nil
}

func checkEndpointsUnique(file *File) error {
	for _, entry := range file.workloads() {
		names := make([]string, 0, len(entry.workload.Endpoints))
		for _, endpoint := range entry.workload.Endpoints {
			names = append(names, endpoint.Name)
		}
		if duplicate, found := firstDuplicate(names); found {
			return fmt.Errorf("%w: %s endpoint %q is declared twice", ErrInvalid, entry.label, duplicate)
		}
	}
	return nil
}

func checkEndpointsOrdered(file *File) error {
	for _, entry := range file.workloads() {
		endpoints := entry.workload.Endpoints
		for index := 1; index < len(endpoints); index++ {
			if endpoints[index-1].Name > endpoints[index].Name {
				return fmt.Errorf("%w: %s endpoints are not in name order (%q after %q)", ErrInvalid, entry.label, endpoints[index].Name, endpoints[index-1].Name)
			}
		}
	}
	return nil
}

func checkEndpointPorts(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			if endpoint.Port > 65535 {
				return fmt.Errorf("%w: %s endpoint %s port %d is not a port", ErrInvalid, entry.label, endpoint.Name, endpoint.Port)
			}
		}
	}
	return nil
}

// visibilities is the endpoint visibility vocabulary a cell may carry: core's
// own declared set, which core#703's cold cutover reduced to these three.
// "module" and "external" are NOT among them — a service declaring either no
// longer loads at all (resources.ValidateEndpointDeclaration refuses it, and
// resources.KnownVisibility reports false), so a cell carrying one describes a
// service that cannot exist, and refusing it here is agreement with core
// rather than strictness.
//
// What a visibility PERMITS is not decided here —
// resources.ValidateEndpointVisibility and the workspace's own validation own
// that. The cell carries the declaration so the platform derives its mesh
// policy from what was rendered, and a spelling no reader knows is a policy
// nobody wrote. TestTheVisibilityVocabularyIsCoreOwn fails if the two drift;
// it imports resources from the TEST binary only, so a loader linking this
// package never pulls core's resource tree in with it.
var visibilities = []string{"private", "internal", "public"}

const (
	// allowAllModules is the allow-list wildcard, from the shared grammar.
	allowAllModules = names.AllowAllModules
	// visibilityInternal is the one visibility whose allow-list is read.
	visibilityInternal = "internal"
)

func checkVisibilities(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			if slices.Contains(visibilities, endpoint.Visibility) {
				continue
			}
			if endpoint.Visibility == "" {
				// An OMITTED visibility is refused, though the resource model
				// admits one: resources.Endpoint.postLoad has already resolved
				// the omission to "private" by the time a render writes a cell,
				// so a cell carrying none describes an endpoint no render
				// produces — and would put that default in a second place, for
				// the platform to re-derive, which is the one thing this file
				// exists not to make it do.
				return fmt.Errorf("%w: %s endpoint %s carries no visibility; a cell states what the platform derives policy from, so the declaration is written out rather than defaulted again here", ErrInvalid, entry.label, endpoint.Name)
			}
			return fmt.Errorf("%w: %s endpoint %s visibility %q is not one of %s", ErrInvalid, entry.label, endpoint.Name, endpoint.Visibility, strings.Join(visibilities, ", "))
		}
	}
	return nil
}

// checkAllowModules holds the allow-list to module names and the wildcard. It
// does NOT require the list to be sorted or deduplicated: the render copies
// the service's own declaration verbatim, and an allow-list is read as a set.
func checkAllowModules(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			for _, allowed := range endpoint.AllowModules {
				// The SAME predicate resources holds a service's declaration
				// to, not a second grammar beside it: this reader accepted
				// "billing.worker" and "billing_worker", which core refuses at
				// the source, so a cell was accepted as a valid policy
				// declaration while describing an allow-list that cannot load.
				if names.IsAllowModulesEntry(allowed) {
					continue
				}
				return fmt.Errorf("%w: %s endpoint %s allow_modules names %q, which is not a module name or %q", ErrInvalid, entry.label, endpoint.Name, allowed, allowAllModules)
			}
		}
	}
	return nil
}

// checkAllowModulesNeedInternal: an allow-list is only read for "internal",
// and resources.ValidateEndpointDeclaration refuses one anywhere else, so a
// cell carrying an allow-list beside "public" or "private" describes a service
// declaration that cannot load — and would hand the platform an allow-list no
// visibility consults.
func checkAllowModulesNeedInternal(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			if len(endpoint.AllowModules) == 0 || endpoint.Visibility == visibilityInternal {
				continue
			}
			return fmt.Errorf("%w: %s endpoint %s lists allow_modules with visibility %q; an allow-list is only read for %q",
				ErrInvalid, entry.label, endpoint.Name, endpoint.Visibility, visibilityInternal)
		}
	}
	return nil
}

func checkConsumersQualified(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			for _, consumer := range endpoint.Consumers {
				if !qualifiedServicePattern.MatchString(consumer) {
					return fmt.Errorf("%w: %s endpoint %s consumer %q is not <module>/<service>", ErrInvalid, entry.label, endpoint.Name, consumer)
				}
			}
		}
	}
	return nil
}

func checkConsumersOrdered(file *File) error {
	for _, entry := range file.workloads() {
		for _, endpoint := range entry.workload.Endpoints {
			if !sort.StringsAreSorted(endpoint.Consumers) {
				return fmt.Errorf("%w: %s endpoint %s consumers are not in order", ErrInvalid, entry.label, endpoint.Name)
			}
			// Sorted is not unique: one declared edge is one entry, so a
			// repeat is a cell saying the same thing twice and a count no
			// reader can trust.
			if duplicate, found := firstDuplicate(endpoint.Consumers); found {
				return fmt.Errorf("%w: %s endpoint %s names consumer %q twice", ErrInvalid, entry.label, endpoint.Name, duplicate)
			}
		}
	}
	return nil
}

func checkIngressServed(file *File) error {
	for _, entry := range file.workloads() {
		for _, route := range entry.workload.Ingress {
			if !slices.ContainsFunc(entry.workload.Endpoints, func(endpoint Endpoint) bool { return endpoint.Name == route.Endpoint }) {
				return fmt.Errorf("%w: %s ingress names endpoint %q, which the workload does not serve", ErrInvalid, entry.label, route.Endpoint)
			}
		}
	}
	return nil
}

func checkIngressHasHost(file *File) error {
	for _, entry := range file.workloads() {
		for _, route := range entry.workload.Ingress {
			if len(route.Hosts) == 0 {
				return fmt.Errorf("%w: %s ingress to %s names no host", ErrInvalid, entry.label, route.Endpoint)
			}
		}
	}
	return nil
}

func checkIngressHostNames(file *File) error {
	for _, entry := range file.workloads() {
		for _, route := range entry.workload.Ingress {
			if duplicate, found := firstDuplicate(route.Hosts); found {
				return fmt.Errorf("%w: %s ingress to %s names host %q twice", ErrInvalid, entry.label, route.Endpoint, duplicate)
			}
			for _, host := range route.Hosts {
				if !isHostName(host) {
					return fmt.Errorf("%w: %s ingress to %s host %q is not a host name", ErrInvalid, entry.label, route.Endpoint, host)
				}
			}
		}
	}
	return nil
}

// isHostName is a DNS host name as a route or an egress names one: a DNS
// subdomain, lowercase, with no port, path or scheme.
func isHostName(host string) bool {
	return isDNS1123Subdomain(host)
}

// checkIngressOrdered: the render sorts ingress by endpoint, and a cell is
// written deterministically — the same rule endpoints and egress already have.
func checkIngressOrdered(file *File) error {
	for _, entry := range file.workloads() {
		for index := 1; index < len(entry.workload.Ingress); index++ {
			if entry.workload.Ingress[index-1].Endpoint > entry.workload.Ingress[index].Endpoint {
				return fmt.Errorf("%w: %s ingress is not in endpoint order (%q after %q)", ErrInvalid, entry.label,
					entry.workload.Ingress[index].Endpoint, entry.workload.Ingress[index-1].Endpoint)
			}
			// One route per endpoint: two routes to one endpoint are two
			// host sets for one thing, and which one the platform renders
			// would be its choice to make.
			if entry.workload.Ingress[index-1].Endpoint == entry.workload.Ingress[index].Endpoint {
				return fmt.Errorf("%w: %s declares ingress to endpoint %q twice; one endpoint has one route, with its hosts together",
					ErrInvalid, entry.label, entry.workload.Ingress[index].Endpoint)
			}
		}
	}
	return nil
}

func checkBindingNames(file *File) error {
	for _, entry := range file.workloads() {
		for _, binding := range entry.workload.Bindings {
			if !namePattern.MatchString(binding) {
				return fmt.Errorf("%w: %s binding %q is not a lowercase name", ErrInvalid, entry.label, binding)
			}
		}
	}
	return nil
}

func checkBindingsUnique(file *File) error {
	for _, entry := range file.workloads() {
		if duplicate, found := firstDuplicate(entry.workload.Bindings); found {
			return fmt.Errorf("%w: %s binding %q is declared twice", ErrInvalid, entry.label, duplicate)
		}
	}
	return nil
}

// --- egress --------------------------------------------------------------

// labelledEgress is one egress entry with its namespace.
type labelledEgress struct {
	namespace *Namespace
	egress    *Egress
}

// egresses lists every egress entry of the cell, in file order.
func (file *File) egresses() []labelledEgress {
	var all []labelledEgress
	for n := range file.Namespaces {
		namespace := &file.Namespaces[n]
		for e := range namespace.Egress {
			all = append(all, labelledEgress{namespace, &namespace.Egress[e]})
		}
	}
	return all
}

func checkEgressQualified(file *File) error {
	for _, entry := range file.egresses() {
		if !qualifiedServicePattern.MatchString(entry.egress.Service) {
			return fmt.Errorf("%w: namespace %s egress service %q is not <module>/<service>", ErrInvalid, entry.namespace.Name, entry.egress.Service)
		}
	}
	return nil
}

// checkEgressOfModule holds every egress entry to the namespace's module: a
// grant located under this namespace is this module's, never attributed to
// another. A local workload is not required — a managed replacement can
// legitimately run none.
func checkEgressOfModule(file *File) error {
	for _, entry := range file.egresses() {
		if module, _, _ := strings.Cut(entry.egress.Service, "/"); module != entry.namespace.Module {
			return fmt.Errorf("%w: namespace %s egress for %q names a service of another module than the namespace's %q", ErrInvalid, entry.namespace.Name, entry.egress.Service, entry.namespace.Module)
		}
	}
	return nil
}

func checkEgressHasTarget(file *File) error {
	for _, entry := range file.egresses() {
		if len(entry.egress.Hosts) == 0 && len(entry.egress.CIDRs) == 0 {
			return fmt.Errorf("%w: namespace %s egress for %s declares neither a host nor a CIDR", ErrInvalid, entry.namespace.Name, entry.egress.Service)
		}
	}
	return nil
}

func checkEgressHostNames(file *File) error {
	for _, entry := range file.egresses() {
		for _, host := range entry.egress.Hosts {
			if !isHostName(host.Name) {
				return fmt.Errorf("%w: namespace %s egress for %s host %q is not a host name", ErrInvalid, entry.namespace.Name, entry.egress.Service, host.Name)
			}
		}
	}
	return nil
}

func checkEgressHostPorts(file *File) error {
	for _, entry := range file.egresses() {
		for _, host := range entry.egress.Hosts {
			if host.Port < 1 || host.Port > 65535 {
				return fmt.Errorf("%w: namespace %s egress for %s host %s port %d is not a port; the port is always explicit", ErrInvalid, entry.namespace.Name, entry.egress.Service, host.Name, host.Port)
			}
		}
	}
	return nil
}

func checkEgressCIDRs(file *File) error {
	for _, entry := range file.egresses() {
		networks := make([]*net.IPNet, 0, len(entry.egress.CIDRs))
		for _, cidr := range entry.egress.CIDRs {
			address, network, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("%w: namespace %s egress for %s CIDR %q: %v", ErrInvalid, entry.namespace.Name, entry.egress.Service, cidr, err)
			}
			// Canonical: 10.20.1.7/16 and 10.20.0.0/16 are one range written
			// two ways, and a platform comparing declared reach to rendered
			// policy would have to canonicalise it itself — a second place
			// the meaning lives.
			if !address.Equal(network.IP) {
				return fmt.Errorf("%w: namespace %s egress for %s CIDR %q is not canonical; the range it names is %q, and one range has one spelling",
					ErrInvalid, entry.namespace.Name, entry.egress.Service, cidr, network.String())
			}
			for _, other := range networks {
				// Stated once: a range inside another, or the same range
				// twice, is reach declared twice, and the narrower statement
				// grants nothing the wider one did not.
				if other.Contains(network.IP) || network.Contains(other.IP) {
					return fmt.Errorf("%w: namespace %s egress for %s declares overlapping CIDRs %q and %q; reach is stated once",
						ErrInvalid, entry.namespace.Name, entry.egress.Service, other.String(), network.String())
				}
			}
			networks = append(networks, network)
		}
	}
	return nil
}

func checkEgressOrdered(file *File) error {
	for _, namespace := range file.Namespaces {
		for index := 1; index < len(namespace.Egress); index++ {
			if namespace.Egress[index-1].Service >= namespace.Egress[index].Service {
				return fmt.Errorf("%w: namespace %s egress is not in service order, or names a service twice (%q after %q)", ErrInvalid, namespace.Name, namespace.Egress[index].Service, namespace.Egress[index-1].Service)
			}
		}
	}
	return nil
}

// --- delivery ------------------------------------------------------------

// deliveries lists every namespace's delivery Job, with the label a refusal
// names it by.
func (file *File) deliveries() []struct {
	label    string
	delivery *Delivery
} {
	var all []struct {
		label    string
		delivery *Delivery
	}
	for n := range file.Namespaces {
		namespace := &file.Namespaces[n]
		if namespace.Delivery != nil {
			all = append(all, struct {
				label    string
				delivery *Delivery
			}{"namespace " + namespace.Name + " delivery", namespace.Delivery})
		}
	}
	return all
}

func checkDeliveryKinds(file *File) error {
	for _, entry := range file.deliveries() {
		if entry.delivery.Kind != KindJob {
			return fmt.Errorf("%w: %s kind %q is not a Job", ErrInvalid, entry.label, entry.delivery.Kind)
		}
	}
	return nil
}

func checkDeliveryAccounts(file *File) error {
	for _, entry := range file.deliveries() {
		if !isDNS1123Subdomain(entry.delivery.ServiceAccount) {
			return fmt.Errorf("%w: %s service_account %q is not a DNS subdomain (lowercase alphanumerics, dashes and dots, at most %d characters)", ErrInvalid, entry.label, entry.delivery.ServiceAccount, dns1123SubdomainMaxLength)
		}
	}
	return nil
}

func checkDeliveryContainerNames(file *File) error {
	for _, entry := range file.deliveries() {
		if !isDNS1123Label(entry.delivery.Container) {
			return fmt.Errorf("%w: %s container %q is not a DNS label (lowercase alphanumerics and dashes, at most %d characters)", ErrInvalid, entry.label, entry.delivery.Container, dns1123LabelMaxLength)
		}
	}
	return nil
}
