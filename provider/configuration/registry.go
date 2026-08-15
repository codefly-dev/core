package configuration

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

const (
	BillingContract                 = "codefly.dev/configuration/billing@1"
	EmailContract                   = "codefly.dev/configuration/email@1"
	ErrorTrackingContract           = "codefly.dev/configuration/error-tracking@1"
	ErrorTrackingBuildContract      = "codefly.dev/configuration/error-tracking-build@1"
	FeatureFlagsContract            = "codefly.dev/configuration/feature-flags@1"
	FeatureFlagsBrowserContract     = "codefly.dev/configuration/feature-flags-browser@1"
	ProductAnalyticsContract        = "codefly.dev/configuration/product-analytics@1"
	ProductAnalyticsBrowserContract = "codefly.dev/configuration/product-analytics-browser@1"
)

type ValueType string

const (
	ValueString            ValueType = "string"
	ValueBoolean           ValueType = "boolean"
	ValueInteger           ValueType = "integer"
	ValueOpaqueReference   ValueType = "opaque-reference"
	ValueEndpointReference ValueType = "endpoint-reference"
)

type Classification string

const (
	ClassificationPublic    Classification = "public"
	ClassificationSensitive Classification = "sensitive"
	ClassificationSecret    Classification = "secret"
)

type CredentialPurpose string

const (
	PurposeNone                CredentialPurpose = "none"
	PurposeManagement          CredentialPurpose = "management"
	PurposeRuntime             CredentialPurpose = "runtime"
	PurposeBuild               CredentialPurpose = "build"
	PurposeWebhookVerification CredentialPurpose = "webhook-verification"
)

type BrowserExposure string

const (
	BrowserDenied  BrowserExposure = "denied"
	BrowserAllowed BrowserExposure = "allowed"
)

type ConsumerClass string

const (
	ConsumerRuntime ConsumerClass = "runtime"
	ConsumerBrowser ConsumerClass = "browser"
	ConsumerBuild   ConsumerClass = "build"
)

type Mutator string

const (
	MutatorHost     Mutator = "host"
	MutatorProvider Mutator = "provider"
)

type Provenance string

const (
	ProvenanceProvider Provenance = "provider"
	ProvenanceBinding  Provenance = "binding"
	ProvenanceArtifact Provenance = "artifact"
	ProvenanceHost     Provenance = "host"
)

type Key struct {
	Type                  ValueType
	Required              bool
	ClassificationFloor   Classification
	ClassificationCeiling Classification
	CredentialPurpose     CredentialPurpose
	BrowserExposure       BrowserExposure
	Consumer              ConsumerClass
	ProviderMutable       bool
	HostMutable           bool
	RequiredProvenance    []Provenance
}

type Contract struct {
	ID   string
	Keys map[string]Key
}

type Value struct {
	Type              ValueType
	String            string
	Boolean           bool
	Integer           int64
	OpaqueReference   string
	SafeFingerprint   string
	Classification    Classification
	CredentialPurpose CredentialPurpose
	BrowserExposure   BrowserExposure
	Consumer          ConsumerClass
	MutatedBy         Mutator
	Provenance        map[Provenance]string
}

type HostAttestations struct {
	OpaqueReferences []*providerv0.OpaqueReference
}

type ValidationContext struct {
	Consumer     ConsumerClass
	Attestations HostAttestations
}

type Registry struct {
	contracts map[string]Contract
}

func NewRegistry() *Registry {
	providerProvenance := []Provenance{ProvenanceProvider, ProvenanceBinding, ProvenanceArtifact}
	hostProvenance := append(append([]Provenance(nil), providerProvenance...), ProvenanceHost)
	return &Registry{contracts: map[string]Contract{
		BillingContract: {
			ID: BillingContract,
			Keys: map[string]Key{
				"STRIPE_PUBLISHABLE_KEY": publicKey(true, ConsumerBrowser, PurposeRuntime, providerProvenance),
				"STRIPE_SECRET_KEY":      opaqueKey(true, ConsumerRuntime, PurposeRuntime, providerProvenance),
				"STRIPE_WEBHOOK_SECRET":  opaqueKey(true, ConsumerRuntime, PurposeWebhookVerification, providerProvenance),
				"STRIPE_PRICE_ID":        publicKey(false, ConsumerRuntime, PurposeNone, providerProvenance),
			},
		},
		EmailContract: {
			ID: EmailContract,
			Keys: map[string]Key{
				"RESEND_API_KEY":       opaqueKey(true, ConsumerRuntime, PurposeRuntime, providerProvenance),
				"EMAIL_FROM":           publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"EMAIL_WEBHOOK_SECRET": opaqueKey(false, ConsumerRuntime, PurposeWebhookVerification, providerProvenance),
			},
		},
		ErrorTrackingContract: {
			ID: ErrorTrackingContract,
			Keys: map[string]Key{
				"SENTRY_DSN": {
					Type: ValueString, Required: true,
					ClassificationFloor: ClassificationPublic, ClassificationCeiling: ClassificationSensitive,
					CredentialPurpose: PurposeRuntime, BrowserExposure: BrowserAllowed, Consumer: ConsumerBrowser,
					ProviderMutable: true, HostMutable: true, RequiredProvenance: providerProvenance,
				},
				"SENTRY_ENVIRONMENT": publicKey(false, ConsumerRuntime, PurposeNone, providerProvenance),
				"SENTRY_RELEASE":     publicKey(false, ConsumerRuntime, PurposeNone, providerProvenance),
			},
		},
		ErrorTrackingBuildContract: {
			ID: ErrorTrackingBuildContract,
			Keys: map[string]Key{
				"SENTRY_AUTH_TOKEN": opaqueKey(true, ConsumerBuild, PurposeBuild, providerProvenance),
				"SENTRY_ORG":        publicKey(true, ConsumerBuild, PurposeNone, providerProvenance),
				"SENTRY_PROJECT":    publicKey(true, ConsumerBuild, PurposeNone, providerProvenance),
			},
		},
		FeatureFlagsContract: {
			ID: FeatureFlagsContract,
			Keys: map[string]Key{
				"FEATURE_FLAGS_SERVER_ENDPOINT":   endpointKey(ConsumerRuntime, hostProvenance),
				"FEATURE_FLAGS_APPLICATION_ID":    publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_ENVIRONMENT_ID":    publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_PROVIDER_MODE":     publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_SERVER_CREDENTIAL": opaqueKey(true, ConsumerRuntime, PurposeRuntime, providerProvenance),
			},
		},
		FeatureFlagsBrowserContract: {
			ID: FeatureFlagsBrowserContract,
			Keys: map[string]Key{
				"FEATURE_FLAGS_EDGE_ENDPOINT":  endpointKey(ConsumerBrowser, hostProvenance),
				"FEATURE_FLAGS_APPLICATION_ID": publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_ENVIRONMENT_ID": publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_PROVIDER_MODE":  publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
				"FEATURE_FLAGS_BROWSER_CREDENTIAL": {
					Type: ValueOpaqueReference, Required: true,
					ClassificationFloor: ClassificationSensitive, ClassificationCeiling: ClassificationSensitive,
					CredentialPurpose: PurposeRuntime, BrowserExposure: BrowserAllowed, Consumer: ConsumerBrowser,
					ProviderMutable: true, HostMutable: true, RequiredProvenance: providerProvenance,
				},
			},
		},
		ProductAnalyticsContract: {
			ID: ProductAnalyticsContract,
			Keys: map[string]Key{
				"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN": endpointKey(ConsumerRuntime, hostProvenance),
				"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY":    opaqueKey(true, ConsumerRuntime, PurposeRuntime, hostProvenance),
				"PRODUCT_ANALYTICS_MODE":                  publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"PRODUCT_ANALYTICS_PROJECT_ID":            publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
				"PRODUCT_ANALYTICS_ENVIRONMENT":           publicKey(true, ConsumerRuntime, PurposeNone, providerProvenance),
			},
		},
		ProductAnalyticsBrowserContract: {
			ID: ProductAnalyticsBrowserContract,
			Keys: map[string]Key{
				"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN": endpointKey(ConsumerBrowser, hostProvenance),
				"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY":    browserCaptureKey(true, providerProvenance),
				"PRODUCT_ANALYTICS_MODE":                   publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
				"PRODUCT_ANALYTICS_PROJECT_ID":             publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
				"PRODUCT_ANALYTICS_ENVIRONMENT":            publicKey(true, ConsumerBrowser, PurposeNone, providerProvenance),
			},
		},
	}}
}

func (r *Registry) Contracts() []string {
	out := make([]string, 0, len(r.contracts))
	for id := range r.contracts {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (r *Registry) Lookup(id string) (Contract, error) {
	if r == nil {
		return Contract{}, fmt.Errorf("configuration contract registry is required")
	}
	contract, ok := r.contracts[id]
	if !ok {
		return Contract{}, fmt.Errorf("unknown configuration contract %q", id)
	}
	return cloneContract(contract), nil
}

func (r *Registry) Validate(id string, values map[string]Value, context ValidationContext) error {
	contract, err := r.Lookup(id)
	if err != nil {
		return err
	}
	if !validConsumer(context.Consumer) {
		return fmt.Errorf("%s: validation consumer %q is unknown", id, context.Consumer)
	}
	for key := range values {
		declaration, declared := contract.Keys[key]
		if !declared {
			return fmt.Errorf("%s: undeclared key %q", id, key)
		}
		if declaration.Consumer != context.Consumer {
			return fmt.Errorf("%s: key %q belongs to the %q consumer, not the %q consumer configuration", id, key, declaration.Consumer, context.Consumer)
		}
	}
	for name, declaration := range contract.Keys {
		if declaration.Consumer != context.Consumer {
			continue
		}
		value, present := values[name]
		if !present {
			if declaration.Required {
				return fmt.Errorf("%s: required key %q is missing", id, name)
			}
			continue
		}
		if err := validateValue(name, declaration, value, context.Attestations); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

func (r *Registry) ValidateProposal(proposal *providerv0.OutputProposal) error {
	if proposal == nil {
		return fmt.Errorf("output proposal is required")
	}
	contract, err := r.Lookup(proposal.GetContract())
	if err != nil {
		return err
	}
	for name := range proposal.GetValues() {
		if _, declared := contract.Keys[name]; !declared {
			return fmt.Errorf("%s: undeclared key %q", contract.ID, name)
		}
	}
	for name, declaration := range contract.Keys {
		value, present := proposal.GetValues()[name]
		if !present {
			if declaration.Required {
				return fmt.Errorf("%s: required key %q is missing", contract.ID, name)
			}
			continue
		}
		if err := validateProposalValue(name, declaration, value); err != nil {
			return fmt.Errorf("%s: %w", contract.ID, err)
		}
	}
	return nil
}

func validateProposalValue(name string, declaration Key, value *providerv0.OutputValue) error {
	if value == nil {
		return fmt.Errorf("%s value is required", name)
	}
	if declaration.Type == ValueOpaqueReference {
		reference := value.GetOpaqueReference()
		if reference == nil {
			return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
		}
		purpose, ok := credentialPurposeFromProto(reference.GetPurpose())
		if !ok {
			return fmt.Errorf("%s has unknown credential purpose %d", name, reference.GetPurpose())
		}
		if purpose != declaration.CredentialPurpose {
			return fmt.Errorf("%s credential purpose %q does not match schema purpose %q", name, purpose, declaration.CredentialPurpose)
		}
		if err := ValidateOpaqueReference(reference.GetReference()); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	public := value.GetPublicValue()
	if public == nil {
		return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
	}
	switch declaration.Type {
	case ValueString:
		kind, ok := public.GetKind().(*providerv0.PublicValue_StringValue)
		if !ok {
			return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
		}
		if kind.StringValue == "" {
			return fmt.Errorf("%s string value is empty", name)
		}
		if (declaration.ClassificationFloor == ClassificationPublic || declaration.BrowserExposure == BrowserAllowed) && LooksSecret(kind.StringValue) {
			return fmt.Errorf("%s contains a secret-shaped value in a public or browser field", name)
		}
	case ValueEndpointReference:
		kind, ok := public.GetKind().(*providerv0.PublicValue_StringValue)
		if !ok {
			return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
		}
		if err := ValidateEndpointReference(kind.StringValue); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	case ValueBoolean:
		if _, ok := public.GetKind().(*providerv0.PublicValue_BoolValue); !ok {
			return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
		}
	case ValueInteger:
		if _, ok := public.GetKind().(*providerv0.PublicValue_IntegerValue); !ok {
			return fmt.Errorf("%s type does not match schema type %q", name, declaration.Type)
		}
	default:
		return fmt.Errorf("%s has unsupported schema type %q", name, declaration.Type)
	}
	return nil
}

func credentialPurposeFromProto(purpose providerv0.CredentialPurpose) (CredentialPurpose, bool) {
	switch purpose {
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT:
		return PurposeManagement, true
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME:
		return PurposeRuntime, true
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD:
		return PurposeBuild, true
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
		return PurposeWebhookVerification, true
	default:
		return "", false
	}
}

func validateValue(name string, declaration Key, value Value, attestations HostAttestations) error {
	if value.Type != declaration.Type {
		return fmt.Errorf("%s type %q does not match schema type %q", name, value.Type, declaration.Type)
	}
	if !validClassification(value.Classification) {
		return fmt.Errorf("%s has unknown classification %q", name, value.Classification)
	}
	if classificationRank(value.Classification) < classificationRank(declaration.ClassificationFloor) {
		return fmt.Errorf("%s attempts to weaken classification below %q", name, declaration.ClassificationFloor)
	}
	if classificationRank(value.Classification) > classificationRank(declaration.ClassificationCeiling) {
		return fmt.Errorf("%s classification exceeds schema ceiling %q", name, declaration.ClassificationCeiling)
	}
	if value.CredentialPurpose != declaration.CredentialPurpose {
		return fmt.Errorf("%s credential purpose %q does not match schema purpose %q", name, value.CredentialPurpose, declaration.CredentialPurpose)
	}
	if !validBrowserExposure(value.BrowserExposure) {
		return fmt.Errorf("%s has unknown browser exposure %q", name, value.BrowserExposure)
	}
	if browserExposureRank(value.BrowserExposure) > browserExposureRank(declaration.BrowserExposure) {
		return fmt.Errorf("%s browser exposure %q exceeds schema ceiling %q", name, value.BrowserExposure, declaration.BrowserExposure)
	}
	if value.Consumer != declaration.Consumer {
		return fmt.Errorf("%s consumer %q does not match schema consumer %q", name, value.Consumer, declaration.Consumer)
	}
	if value.Consumer == ConsumerRuntime && (value.CredentialPurpose == PurposeManagement || value.CredentialPurpose == PurposeBuild) {
		return fmt.Errorf("%s cannot expose management or build credentials to runtime", name)
	}
	if value.Consumer == ConsumerBrowser && value.Classification == ClassificationSecret {
		return fmt.Errorf("%s cannot expose a secret classification to browser", name)
	}
	switch value.MutatedBy {
	case MutatorProvider:
		if !declaration.ProviderMutable {
			return fmt.Errorf("%s is not provider mutable", name)
		}
	case MutatorHost:
		if !declaration.HostMutable {
			return fmt.Errorf("%s is not host mutable", name)
		}
	default:
		return fmt.Errorf("%s has unknown mutator %q", name, value.MutatedBy)
	}
	for _, required := range declaration.RequiredProvenance {
		if strings.TrimSpace(value.Provenance[required]) == "" {
			return fmt.Errorf("%s is missing %s provenance", name, required)
		}
	}
	switch value.Type {
	case ValueString, ValueEndpointReference:
		if value.String == "" {
			return fmt.Errorf("%s string value is empty", name)
		}
		if (value.Classification == ClassificationPublic || value.BrowserExposure == BrowserAllowed) && LooksSecret(value.String) {
			return fmt.Errorf("%s contains a secret-shaped value in a public or browser field", name)
		}
		if value.OpaqueReference != "" || value.SafeFingerprint != "" {
			return fmt.Errorf("%s string value cannot also carry an opaque reference", name)
		}
		if value.Type == ValueEndpointReference {
			if err := ValidateEndpointReference(value.String); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	case ValueOpaqueReference:
		if value.OpaqueReference == "" || value.String != "" {
			return fmt.Errorf("%s must contain only an opaque reference", name)
		}
		if err := ValidateOpaqueReference(value.OpaqueReference); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		attested := opaqueReference(value.OpaqueReference, attestations.OpaqueReferences)
		if attested == nil {
			return fmt.Errorf("%s opaque reference is not host-attested", name)
		}
		attestedPurpose, known := credentialPurposeFromProto(attested.GetPurpose())
		if !known || attestedPurpose != declaration.CredentialPurpose {
			return fmt.Errorf("%s host-attested purpose %q does not match schema purpose %q", name, attested.GetPurpose(), declaration.CredentialPurpose)
		}
		if !digestPattern.MatchString(value.SafeFingerprint) || value.SafeFingerprint != attested.GetSafeFingerprint() {
			return fmt.Errorf("%s safe fingerprint does not match its host attestation", name)
		}
		if slices.Contains(declaration.RequiredProvenance, ProvenanceHost) && value.Provenance[ProvenanceHost] != attested.GetSafeFingerprint() {
			return fmt.Errorf("%s host provenance does not match its opaque reference", name)
		}
	case ValueBoolean, ValueInteger:
		if value.String != "" || value.OpaqueReference != "" || value.SafeFingerprint != "" {
			return fmt.Errorf("%s scalar value carries an incompatible string field", name)
		}
	default:
		return fmt.Errorf("%s has unknown value type %q", name, value.Type)
	}
	return nil
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^sk_(live|test)_[A-Za-z0-9]{8,}$`),
	regexp.MustCompile(`(?i)^rk_(live|test)_[A-Za-z0-9]{8,}$`),
	regexp.MustCompile(`(?i)^phx_[A-Za-z0-9_-]{8,}$`),
	regexp.MustCompile(`(?i)^bearer\s+\S+$`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(password|client_secret|access_token|api_key)=`),
}

func LooksSecret(value string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func publicKey(required bool, consumer ConsumerClass, purpose CredentialPurpose, provenance []Provenance) Key {
	exposure := BrowserDenied
	if consumer == ConsumerBrowser {
		exposure = BrowserAllowed
	}
	return Key{
		Type: ValueString, Required: required,
		ClassificationFloor: ClassificationPublic, ClassificationCeiling: ClassificationSensitive,
		CredentialPurpose: purpose, BrowserExposure: exposure, Consumer: consumer,
		ProviderMutable: true, HostMutable: true, RequiredProvenance: provenance,
	}
}

func opaqueKey(required bool, consumer ConsumerClass, purpose CredentialPurpose, provenance []Provenance) Key {
	return Key{
		Type: ValueOpaqueReference, Required: required,
		ClassificationFloor: ClassificationSecret, ClassificationCeiling: ClassificationSecret,
		CredentialPurpose: purpose, BrowserExposure: BrowserDenied, Consumer: consumer,
		ProviderMutable: true, HostMutable: true, RequiredProvenance: provenance,
	}
}

func endpointKey(consumer ConsumerClass, provenance []Provenance) Key {
	exposure := BrowserDenied
	if consumer == ConsumerBrowser {
		exposure = BrowserAllowed
	}
	return Key{
		Type: ValueEndpointReference, Required: true,
		ClassificationFloor: ClassificationPublic, ClassificationCeiling: ClassificationSensitive,
		CredentialPurpose: PurposeNone, BrowserExposure: exposure, Consumer: consumer,
		ProviderMutable: false, HostMutable: true, RequiredProvenance: provenance,
	}
}

func browserCaptureKey(required bool, provenance []Provenance) Key {
	return Key{
		Type: ValueString, Required: required,
		ClassificationFloor: ClassificationPublic, ClassificationCeiling: ClassificationPublic,
		CredentialPurpose: PurposeRuntime, BrowserExposure: BrowserAllowed, Consumer: ConsumerBrowser,
		ProviderMutable: true, HostMutable: true, RequiredProvenance: provenance,
	}
}

func validClassification(classification Classification) bool {
	return classification == ClassificationPublic || classification == ClassificationSensitive || classification == ClassificationSecret
}

func classificationRank(classification Classification) int {
	switch classification {
	case ClassificationPublic:
		return 1
	case ClassificationSensitive:
		return 2
	case ClassificationSecret:
		return 3
	default:
		return 0
	}
}

func validBrowserExposure(exposure BrowserExposure) bool {
	return exposure == BrowserDenied || exposure == BrowserAllowed
}

func browserExposureRank(exposure BrowserExposure) int {
	if exposure == BrowserAllowed {
		return 1
	}
	return 0
}

func validConsumer(consumer ConsumerClass) bool {
	return consumer == ConsumerRuntime || consumer == ConsumerBrowser || consumer == ConsumerBuild
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func opaqueReference(value string, attestations []*providerv0.OpaqueReference) *providerv0.OpaqueReference {
	for _, attestation := range attestations {
		if attestation != nil && attestation.GetReference() == value {
			return attestation
		}
	}
	return nil
}

var opaqueReferenceComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

func ValidateEndpointReference(reference string) error {
	if len(reference) == 0 || len(reference) > 512 || strings.ContainsAny(reference, " \t\r\n%?#") {
		return fmt.Errorf("endpoint reference is not canonical")
	}
	parsed, err := url.Parse(reference)
	if err != nil || parsed.Scheme != "endpoint" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("endpoint reference is not canonical")
	}
	if parsed.Host == "" || parsed.Hostname() != parsed.Host || !opaqueReferenceComponent.MatchString(parsed.Host) {
		return fmt.Errorf("endpoint reference origin rule is invalid")
	}
	components := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(components) != 1 || !opaqueReferenceComponent.MatchString(components[0]) {
		return fmt.Errorf("endpoint reference endpoint identifier is invalid")
	}
	return nil
}

func ValidateOpaqueReference(reference string) error {
	if len(reference) == 0 || len(reference) > 512 || strings.ContainsAny(reference, " \t\r\n%") {
		return fmt.Errorf("opaque reference is not a canonical host reference")
	}
	parsed, err := url.Parse(reference)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("opaque reference is not a canonical host reference")
	}
	switch parsed.Scheme {
	case "secret", "capture", "opaque":
	default:
		return fmt.Errorf("opaque reference uses an unknown host namespace")
	}
	if parsed.Host == "" || parsed.Hostname() != parsed.Host || !opaqueReferenceComponent.MatchString(parsed.Host) || LooksSecret(parsed.Host) {
		return fmt.Errorf("opaque reference host identifier is invalid")
	}
	for _, component := range strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/") {
		if component == "" {
			if parsed.Path == "" {
				continue
			}
			return fmt.Errorf("opaque reference path identifier is invalid")
		}
		if !opaqueReferenceComponent.MatchString(component) || LooksSecret(component) {
			return fmt.Errorf("opaque reference path identifier is invalid")
		}
	}
	return nil
}

func cloneContract(contract Contract) Contract {
	out := Contract{ID: contract.ID, Keys: make(map[string]Key, len(contract.Keys))}
	for name, key := range contract.Keys {
		key.RequiredProvenance = append([]Provenance(nil), key.RequiredProvenance...)
		out.Keys[name] = key
	}
	return out
}
