package resources

import (
	"embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed testdata/endpoints/declarations/*.yaml
var endpointDeclarationFixtures embed.FS

// EndpointDeclarationOutcome is what a reader must do with a fixture.
type EndpointDeclarationOutcome string

const (
	// EndpointDeclarationAccepted means the manifest loads.
	EndpointDeclarationAccepted EndpointDeclarationOutcome = "accepted"
	// EndpointDeclarationRefused means the manifest is refused with
	// ErrInvalidEndpointDeclaration, and a message carrying the fixture's.
	EndpointDeclarationRefused EndpointDeclarationOutcome = "refused"
)

// EndpointDeclarationFixture is one service manifest of the endpoint
// declaration kit and the verdict every reader must reach on it.
type EndpointDeclarationFixture struct {
	// Name identifies the fixture in a failure.
	Name string
	// Document is the manifest's bytes, as a service would declare them.
	Document []byte
	// Outcome is the verdict.
	Outcome EndpointDeclarationOutcome
	// Message is text a refusal must carry: one reason for one rule,
	// whichever reader refused it.
	Message string
	// Rule names the rule a refused fixture protects: the one a reader cannot
	// drop without this fixture noticing. Empty for an accepted fixture.
	Rule string
}

// EndpointDeclarationFixtures is the kit: every accepted and refused service
// manifest, with the verdict each must reach. Every rule the endpoint model
// enforces on a manifest — the decoder rules on its keys, the declaration
// rules on its values — is protected by at least one refused fixture here,
// which the package's own tests prove by deleting each rule in turn; the one
// rule a manifest cannot reach (wire-fields-known) is protected by the wire
// kit (EndpointWireFixtures).
func EndpointDeclarationFixtures() ([]EndpointDeclarationFixture, error) {
	table := []struct {
		name, message, rule string
		outcome             EndpointDeclarationOutcome
	}{
		{name: "valid", outcome: EndpointDeclarationAccepted},
		{name: "exposed-public", outcome: EndpointDeclarationAccepted},
		{name: "external-private", outcome: EndpointDeclarationAccepted},
		{name: "exposure-none-on-internal", outcome: EndpointDeclarationAccepted},

		{name: "visibility-module", outcome: EndpointDeclarationRefused, message: `unsupported visibility "module"`, rule: ruleVisibilityKnown},
		{name: "visibility-external", outcome: EndpointDeclarationRefused, message: `unsupported visibility "external"`, rule: ruleVisibilityKnown},
		{name: "visibility-unknown", outcome: EndpointDeclarationRefused, message: `unsupported visibility "application"`, rule: ruleVisibilityKnown},
		{name: "location-unknown", outcome: EndpointDeclarationRefused, message: `unsupported location "nowhere"`, rule: ruleLocationKnown},
		{name: "exposure-unknown", outcome: EndpointDeclarationRefused, message: `unsupported exposure "ingress"`, rule: ruleExposureKnown},
		{name: "exposure-omitted-on-public", outcome: EndpointDeclarationRefused, message: `declares visibility "public" but states no exposure`, rule: ruleExposureDeclared},
		{name: "endpoint-key-unknown", outcome: EndpointDeclarationRefused, message: `declares unknown key "visibilty"`, rule: ruleEndpointKeysKnown},
		// The forbidden key is refused by PRESENCE, before decoding: any value
		// (a module, the wildcard, an empty list, null) and any spelling.
		{name: "allow-modules-named", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow-modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-wildcard", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow-modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-on-public", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow-modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-empty", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow-modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-null", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow-modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-underscore", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allow_modules")`, rule: ruleAllowModulesDerived},
		{name: "allow-modules-camel", outcome: EndpointDeclarationRefused, message: `authors allow-modules (key "allowModules")`, rule: ruleAllowModulesDerived},
		{name: "exposure-on-internal", outcome: EndpointDeclarationRefused, message: `exposure "public" with visibility "internal"`, rule: ruleExposureWithinReach},
		{name: "exposure-on-private", outcome: EndpointDeclarationRefused, message: `exposure "public" with visibility ""`, rule: ruleExposureWithinReach},
		{name: "exposure-on-external", outcome: EndpointDeclarationRefused, message: `exposure "public" with location "external"`, rule: ruleExposureInSystem},
	}
	result := make([]EndpointDeclarationFixture, 0, len(table))
	for _, entry := range table {
		document, err := endpointDeclarationFixtures.ReadFile("testdata/endpoints/declarations/" + entry.name + ".yaml")
		if err != nil {
			return nil, fmt.Errorf("endpoint declaration fixture %s: %w", entry.name, err)
		}
		result = append(result, EndpointDeclarationFixture{Name: entry.name, Document: document, Outcome: entry.outcome, Message: entry.message, Rule: entry.rule})
	}
	return result, nil
}

// KitTestingT is the part of *testing.T a kit uses, so importing this package
// does not pull the testing flag set into a consumer's binary.
type KitTestingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// RunEndpointDeclarationKit drives a reader's entrypoint through every fixture
// and fails the test on the first outcome that differs. A consumer passes the
// function it actually loads service manifests with — the renderer's load, the
// CLI's — never ValidateEndpointDeclaration itself, which proves nothing about
// the consumer: the kit exists to show that a path a consumer reads
// declarations through refuses what core refuses, by the same rule.
func RunEndpointDeclarationKit(t KitTestingT, read func(document []byte) error) {
	t.Helper()
	if read == nil {
		t.Fatalf("endpoint declaration conformance: no entrypoint given")
		return
	}
	all, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatalf("endpoint declaration conformance: %v", err)
		return
	}
	for _, fixture := range all {
		err := read(fixture.Document)
		switch fixture.Outcome {
		case EndpointDeclarationAccepted:
			if err != nil {
				t.Errorf("endpoint declaration fixture %s must be accepted, got: %v", fixture.Name, err)
			}
		case EndpointDeclarationRefused:
			switch {
			case err == nil:
				t.Errorf("endpoint declaration fixture %s must be refused with %v (rule %s)", fixture.Name, ErrInvalidEndpointDeclaration, fixture.Rule)
			case !errors.Is(err, ErrInvalidEndpointDeclaration):
				t.Errorf("endpoint declaration fixture %s must be refused with %v (rule %s), got: %v", fixture.Name, ErrInvalidEndpointDeclaration, fixture.Rule, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("endpoint declaration fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, fixture.Rule, err)
			}
		}
	}
}
