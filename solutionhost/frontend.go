package solutionhost

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/wire"
)

// Frontend is what a module or solution declares about the page and the client surfaces
// a host serves for it: the author-declared half of the manifest the host's
// registry projects. The host adds its own constants — the asset path, the
// backend alias, the capability path — and nothing here names an address.
//
// It is a DECLARATION, carried from the one file a component declares it in,
// beside its backend service, which its runtime embeds and serves its
// manifest route from. The CLI reads the same file into the presence document,
// so the host learns a component's frontend from the applied generation and not
// from anything the running process reports.
//
// The block is OPTIONAL and follows BuildSize's precedent exactly: a document
// without it is accepted and encodes as it did before the block existed, so
// every digest a host stored still holds; one that carries it is held to every
// rule in the table. A host older than this block refuses a document carrying
// it as an unknown field, which is strict decoding doing its job and the
// reason core releases before the renderer in a cutover. Both modules and
// solutions may contribute a page; their kind describes responsibility, not
// their mounting mechanism. A tombstone declares nothing to serve.
type Frontend struct {
	// Title is what the host's navigation labels the component with.
	Title string `yaml:"title" json:"title"`

	// Order is where the component sorts in that navigation. Declared, zero
	// included: an omitted order would decode as zero and read as a choice.
	Order int64 `yaml:"order" json:"order"`

	// ExposedModule is the module the component's Module Federation remote
	// exposes as its page, "./Page" by convention.
	ExposedModule string `yaml:"exposed_module" json:"exposed_module"`

	// HostContract is the major of the host page contract the frontend is
	// built against, at least 1. A host refuses a remote built against a
	// major it does not serve, before loading it.
	HostContract uint64 `yaml:"host_contract" json:"host_contract"`

	// Dashboard is the component's data graph — the events, metrics and
	// dashboards the host renders — carried as OPAQUE JSON: an object whose
	// values are strings, whole numbers written in decimal digits, booleans,
	// lists and objects, and nothing else, so what is signed is byte for byte
	// what was written. Its schema is the host's; core bounds its size to
	// FrontendDashboardMaxBytes of canonical encoding and reads nothing in it.
	// Absent when the component declares none, and absent is not an empty
	// object: an empty one is carried as declared.
	Dashboard any `yaml:"dashboard,omitempty" json:"dashboard,omitempty"`

	// Surfaces are what the component offers inside a client application, each
	// one addressed by its client and its id. Absent when it offers none.
	Surfaces []FrontendSurface `yaml:"surfaces,omitempty" json:"surfaces,omitempty"`
}

// FrontendSurface is one offering a module or solution makes inside a client
// application: what the client labels it with, and the module it loads.
type FrontendSurface struct {
	// ID is unique per client: lowercase letters and digits, dash-separated.
	ID string `yaml:"id" json:"id"`

	// Client is the kind of client application the surface is offered to. The
	// vocabulary is the host client's; this document holds the name's shape.
	Client string `yaml:"client" json:"client"`

	// Title is what the client labels the surface with.
	Title string `yaml:"title" json:"title"`

	// Description is the longer explanation a client may show beside the
	// title. Optional.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Module is the path, on the component's own origin, of the module the
	// client loads: an absolute path, never a URL with a scheme or a host.
	Module string `yaml:"module" json:"module"`

	// Contract is the major of the surface contract the client owns, which
	// the module is built against, at least 1: a client runs a surface only
	// when it recognises the major it declares. The host never reads it.
	Contract uint64 `yaml:"contract" json:"contract"`

	// Applies is which documents the surface applies to, as the client
	// understands them: the string "always", or an object {tagged: [...]} of
	// one or more non-empty tags — exactly the two shapes the client has a
	// branch for, and declared, never defaulted. It is carried as the JSON it
	// is (a string, or map[string]any{"tagged": []any{...}}), since the host
	// projects it to the client verbatim.
	Applies any `yaml:"applies" json:"applies"`

	// Events are the journal namespaces the client reconciles the surface on,
	// each a lowercase dotted name, once. Absent when there are none.
	Events []string `yaml:"events,omitempty" json:"events,omitempty"`
}

// FrontendDashboardMaxBytes bounds a dashboard's canonical encoding. A data
// graph is a declaration of events, metrics and widgets, not a store of data,
// and a host persists the applied generation whole.
const FrontendDashboardMaxBytes = 64 << 10

// Frontend limits bound the declaration a host persists and projects. Text
// limits count UTF-8 bytes; collection limits apply to surfaces and to each
// surface's events and tags. The aggregate includes the dashboard.
const (
	FrontendTextMaxBytes       = 1024
	FrontendCollectionMaxItems = 64
	FrontendMaxBytes           = 128 << 10
)

// The frontend rules, by name; see rule.
const (
	ruleFrontendFieldsDeclared = "frontend-fields-declared"
	ruleFrontendNumbersDecimal = "frontend-numbers-decimal"
	ruleFrontendNumbersFit     = "frontend-numbers-fit"
	ruleFrontendDashboardJSON  = "frontend-dashboard-json"

	ruleFrontendTextBounded        = "frontend-text-bounded"
	ruleFrontendCollectionsBounded = "frontend-collections-bounded"
	ruleFrontendBounded            = "frontend-bounded"
	ruleFrontendAbsentWhenRemoved  = "frontend-absent-when-removed"
	ruleFrontendTexts              = "frontend-texts"
	ruleFrontendHostContractMajor  = "frontend-host-contract-major"
	ruleFrontendSurfaceID          = "frontend-surface-id"
	ruleFrontendSurfaceClient      = "frontend-surface-client"
	ruleFrontendSurfaceUnique      = "frontend-surface-unique"
	ruleFrontendSurfaceModule      = "frontend-surface-module"
	ruleFrontendSurfaceContract    = "frontend-surface-contract-major"
	ruleFrontendSurfaceApplies     = "frontend-surface-applies"
	ruleFrontendSurfaceEventName   = "frontend-surface-event-name"
	ruleFrontendSurfaceEventOnce   = "frontend-surface-event-once"
	ruleFrontendDashboardBounded   = "frontend-dashboard-bounded"
)

// frontendNodeRules run over the frontend node before it is typed, and
// frontendModelRules over the decoded block; both are rows of the one table in
// rules(), which is where their order among the other rules is fixed.
func frontendNodeRules() []rule {
	return []rule{
		{name: ruleFrontendFieldsDeclared, section: sectionFrontend, node: checkFrontendFieldsDeclared},
		{name: ruleFrontendNumbersDecimal, section: sectionFrontend, node: checkFrontendNumbersDecimal},
		{name: ruleFrontendNumbersFit, section: sectionFrontend, node: checkFrontendNumbersFit},
		{name: ruleFrontendDashboardJSON, section: sectionFrontend, node: checkFrontendDashboardJSON},
	}
}

func frontendModelRules() []rule {
	return []rule{
		{name: ruleFrontendAbsentWhenRemoved, section: sectionFrontend, check: checkFrontendAbsentWhenRemoved},
		{name: ruleFrontendCollectionsBounded, section: sectionFrontend, check: checkFrontendCollectionsBounded},
		{name: ruleFrontendTextBounded, section: sectionFrontend, check: checkFrontendTextBounded},
		{name: ruleFrontendTexts, section: sectionFrontend, check: checkFrontendTexts},
		{name: ruleFrontendHostContractMajor, section: sectionFrontend, check: checkFrontendHostContractMajor},
		{name: ruleFrontendSurfaceID, section: sectionFrontend, check: checkFrontendSurfaceID},
		{name: ruleFrontendSurfaceClient, section: sectionFrontend, check: checkFrontendSurfaceClient},
		{name: ruleFrontendSurfaceUnique, section: sectionFrontend, check: checkFrontendSurfaceUnique},
		{name: ruleFrontendSurfaceModule, section: sectionFrontend, check: checkFrontendSurfaceModule},
		{name: ruleFrontendSurfaceContract, section: sectionFrontend, check: checkFrontendSurfaceContract},
		{name: ruleFrontendSurfaceApplies, section: sectionFrontend, check: checkFrontendSurfaceApplies},
		{name: ruleFrontendSurfaceEventName, section: sectionFrontend, check: checkFrontendSurfaceEventName},
		{name: ruleFrontendSurfaceEventOnce, section: sectionFrontend, check: checkFrontendSurfaceEventOnce},
		{name: ruleFrontendDashboardBounded, section: sectionFrontend, check: checkFrontendDashboardBounded},
		{name: ruleFrontendBounded, section: sectionFrontend, check: checkFrontendBounded},
	}
}

// frontendFields lists the required fields of the block and of every surface
// that is a mapping, by the path a refusal names: the four fields of the
// block, and each surface's id, client, title, module, contract and applies.
// The numbers are returned apart, for the rules that read a number's
// spelling; order is the one signed number, by its path. A block or surface
// that is not a mapping, and a surfaces value that is not a list, are left to
// the typed decoder, which refuses their shape.
func frontendFields(section *yaml.Node) (numbers, others []countField) {
	for _, name := range []string{"title", "exposed_module"} {
		others = append(others, countField{path: "frontend." + name, node: mappingValue(section, name)})
	}
	for _, name := range []string{"order", "host_contract"} {
		numbers = append(numbers, countField{path: "frontend." + name, node: mappingValue(section, name)})
	}
	rows := wire.Resolve(mappingValue(section, "surfaces"))
	if rows == nil || rows.Kind != yaml.SequenceNode {
		return numbers, others
	}
	for index, row := range rows.Content {
		row = wire.Resolve(row)
		if row == nil || row.Kind != yaml.MappingNode {
			continue
		}
		at := fmt.Sprintf("frontend.surfaces[%d]", index)
		for _, name := range []string{"id", "client", "title", "module", "applies"} {
			others = append(others, countField{path: at + "." + name, node: mappingValue(row, name)})
		}
		numbers = append(numbers, countField{path: at + ".contract", node: mappingValue(row, "contract")})
	}
	return numbers, others
}

// signedNumber reports whether a number of the block may carry a sign: only
// the navigation order; a contract major and a surface's contract are counts.
func signedNumber(path string) bool { return path == "frontend.order" }

func checkFrontendFieldsDeclared(section *yaml.Node) error {
	numbers, others := frontendFields(section)
	for _, field := range append(numbers, others...) {
		if field.node == nil {
			return fmt.Errorf("%w: %s must be declared; an absent field decodes as zero or empty, and a value nobody wrote would be signed as a choice", ErrInvalid, field.path)
		}
	}
	return nil
}

// isDecimalInteger reports whether a present number node is a whole number
// written in decimal digits, with at most a leading minus sign when signed is
// set: the one spelling the canonical encoding writes, so a hexadecimal, octal,
// plus-prefixed or negative-zero spelling is refused rather than read into a
// number nobody wrote. A string is refused, since it is not a number.
func isDecimalInteger(node *yaml.Node, signed bool) bool {
	node = wire.Resolve(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return false
	}
	digits, negative := strings.CutPrefix(node.Value, "-")
	if negative && !signed || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return false
	}
	if digits == "0" {
		return !negative
	}
	return digits[0] != '0'
}

func checkFrontendNumbersDecimal(section *yaml.Node) error {
	numbers, _ := frontendFields(section)
	for _, field := range numbers {
		if field.node != nil && !isDecimalInteger(field.node, signedNumber(field.path)) {
			return fmt.Errorf("%w: %s is %q, not a whole number written in decimal digits; a number has one spelling here, and yaml would otherwise read a base prefix, a leading zero, a plus sign or a string into a number nobody wrote", ErrInvalid, field.path, wire.Resolve(field.node).Value)
		}
	}
	return nil
}

// fitsInteger reports whether a decimal integer scalar fits the field it is
// read into: int64 for the signed order, uint64 for the host contract and for
// a dashboard number that carries no sign, int64 for one that does.
func fitsInteger(node *yaml.Node, signed bool) bool {
	value := wire.Resolve(node).Value
	if signed || strings.HasPrefix(value, "-") {
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func checkFrontendNumbersFit(section *yaml.Node) error {
	numbers, _ := frontendFields(section)
	for _, field := range numbers {
		signed := signedNumber(field.path)
		if field.node == nil || !isDecimalInteger(field.node, signed) {
			continue
		}
		if !fitsInteger(field.node, signed) {
			return fmt.Errorf("%w: %s is %s, more than the field can hold", ErrInvalid, field.path, wire.Resolve(field.node).Value)
		}
	}
	return nil
}

// dashboardDefect walks a dashboard node and names the first node that is not
// JSON the signing encoding carries byte for byte, or "" when there is none.
// The dashboard is an object; a value is a string, a boolean, a whole number
// in decimal digits that fits, a list or an object. Everything else yaml can
// spell is refused by its tag: a timestamp decodes to a time and is re-encoded
// as a different string, binary bytes may not be UTF-8, which JSON rewrites,
// and a number in another base is respelled. Keys are names already, through
// the shared wire check.
func dashboardDefect(node *yaml.Node, at string) string {
	node = wire.Resolve(node)
	switch {
	case node == nil:
		return ""
	case node.Kind == yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			if defect := dashboardDefect(node.Content[index+1], at+"."+wire.Resolve(node.Content[index]).Value); defect != "" {
				return defect
			}
		}
		return ""
	case node.Kind == yaml.SequenceNode:
		for index, item := range node.Content {
			if defect := dashboardDefect(item, fmt.Sprintf("%s[%d]", at, index)); defect != "" {
				return defect
			}
		}
		return ""
	case node.Tag == "!!str" || node.Tag == "!!bool":
		return ""
	case node.Tag == "!!int" && isDecimalInteger(node, true) && fitsInteger(node, false):
		return ""
	case node.Tag == "!!int":
		return fmt.Sprintf("%s is %q, which is not a whole number written in decimal digits that fits", at, node.Value)
	default:
		return fmt.Sprintf("%s is a %s, which JSON cannot carry as written", at, strings.TrimPrefix(node.Tag, "!!"))
	}
}

func checkFrontendDashboardJSON(section *yaml.Node) error {
	dashboard := wire.Resolve(mappingValue(section, "dashboard"))
	if dashboard == nil {
		return nil
	}
	defect := "frontend.dashboard is not an object"
	if dashboard.Kind == yaml.MappingNode {
		defect = dashboardDefect(dashboard, "frontend.dashboard")
	}
	if defect != "" {
		return fmt.Errorf("%w: %s; a dashboard is opaque JSON, signed byte for byte as it was written, so it holds only what JSON spells one way", ErrInvalid, defect)
	}
	return nil
}

func checkFrontendAbsentWhenRemoved(document *SolutionHostBinding) error {
	if document.Removed {
		return fmt.Errorf("%w: a removed generation declares no frontend; a frontend is what a host serves for a build, and this generation declares that there is none", ErrInvalid)
	}
	return nil
}

// frontendText is one text field of the block, by the path a refusal names,
// its value, and whether it may be empty.
type frontendText struct {
	path, value string
	optional    bool
}

func (frontend *Frontend) texts() []frontendText {
	texts := []frontendText{
		{path: "frontend.title", value: frontend.Title},
		{path: "frontend.exposed_module", value: frontend.ExposedModule},
	}
	for index, surface := range frontend.Surfaces {
		at := fmt.Sprintf("frontend.surfaces[%d]", index)
		texts = append(texts,
			frontendText{path: at + ".title", value: surface.Title},
			frontendText{path: at + ".description", value: surface.Description, optional: true})
	}
	return texts
}

// isText reports a value a host can show as one line: non-empty, and free of
// control characters, so a title cannot carry a newline into a navigation
// entry or a pasted block into a signed declaration.
func isText(value string) bool {
	return value != "" && !strings.ContainsFunc(value, unicode.IsControl)
}

func checkFrontendTexts(document *SolutionHostBinding) error {
	for _, text := range document.Frontend.texts() {
		if text.optional && text.value == "" {
			continue
		}
		if !isText(text.value) {
			return fmt.Errorf("%w: %s must be a non-empty single-line text, got %q", ErrInvalid, text.path, text.value)
		}
	}
	return nil
}

func checkFrontendHostContractMajor(document *SolutionHostBinding) error {
	if document.Frontend.HostContract < 1 {
		return fmt.Errorf("%w: frontend.host_contract is %d, which is not a host contract major; a host serves a remote only when it recognises the major it declares, and 0 is one no host serves", ErrInvalid, document.Frontend.HostContract)
	}
	return nil
}

// surfaceIDPattern is a surface's id: lowercase letters and digits, separated
// by single dashes, as the solution runtime spells it.
var surfaceIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func checkFrontendSurfaceID(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		if !surfaceIDPattern.MatchString(surface.ID) {
			return fmt.Errorf("%w: frontend.surfaces[%d].id %q is invalid: lowercase letters and digits, separated by dashes", ErrInvalid, index, surface.ID)
		}
	}
	return nil
}

func checkFrontendSurfaceClient(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		if !segmentPattern.MatchString(surface.Client) {
			return fmt.Errorf("%w: frontend.surfaces[%d].client %q is invalid: a client kind is a lowercase name", ErrInvalid, index, surface.Client)
		}
	}
	return nil
}

func checkFrontendSurfaceUnique(document *SolutionHostBinding) error {
	seen := make(map[string]struct{}, len(document.Frontend.Surfaces))
	for _, surface := range document.Frontend.Surfaces {
		key := surface.Client + "/" + surface.ID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: frontend surface %q is declared twice for client %q; a client addresses a surface by id, so two of its own cannot share one", ErrInvalid, surface.ID, surface.Client)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// isOriginPath reports a path on the component's own origin: absolute, not
// protocol-relative, free of backslashes and control characters, and parsing
// as a URL with neither scheme nor host.
func isOriginPath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r == '\\' || unicode.IsControl(r) }) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "" && parsed.Host == ""
}

func checkFrontendSurfaceModule(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		if !isOriginPath(surface.Module) {
			return fmt.Errorf("%w: frontend.surfaces[%d].module %q is not a path on the component's own origin: it is absolute, and names no scheme and no host", ErrInvalid, index, surface.Module)
		}
	}
	return nil
}

func checkFrontendSurfaceContract(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		if surface.Contract < 1 {
			return fmt.Errorf("%w: frontend.surfaces[%d].contract is %d, which is not a surface contract major; a client runs a surface only when it recognises the major it declares, and 0 is one no client runs", ErrInvalid, index, surface.Contract)
		}
	}
	return nil
}

// isApplies reports one of the two shapes a client has a branch for: the
// string "always", or an object with the one key tagged holding a non-empty
// list of non-empty one-line tags. The reader types a YAML mapping as
// map[string]any and a list as []any, which is also what a producer decoding
// the declared file builds, so both are the one shape judged here.
func isApplies(value any) bool {
	if text, ok := value.(string); ok {
		return text == "always"
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return false
	}
	tags, ok := object["tagged"].([]any)
	if !ok || len(tags) == 0 {
		return false
	}
	for _, tag := range tags {
		if text, ok := tag.(string); !ok || !isText(text) {
			return false
		}
	}
	return true
}

func checkFrontendSurfaceApplies(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		if !isApplies(surface.Applies) {
			return fmt.Errorf("%w: frontend.surfaces[%d].applies is neither \"always\" nor {tagged: [one or more tags]}; a client has a branch for exactly those two shapes, and the host hands it nothing else", ErrInvalid, index)
		}
	}
	return nil
}

func checkFrontendSurfaceEventName(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		for _, event := range surface.Events {
			if !segmentPattern.MatchString(event) {
				return fmt.Errorf("%w: frontend.surfaces[%d].events names %q, which is not a journal namespace: a lowercase dotted name", ErrInvalid, index, event)
			}
		}
	}
	return nil
}

func checkFrontendSurfaceEventOnce(document *SolutionHostBinding) error {
	for index, surface := range document.Frontend.Surfaces {
		seen := make(map[string]struct{}, len(surface.Events))
		for _, event := range surface.Events {
			if _, exists := seen[event]; exists {
				return fmt.Errorf("%w: frontend.surfaces[%d].events names %q twice; a namespace reconciled on once is reconciled on", ErrInvalid, index, event)
			}
			seen[event] = struct{}{}
		}
	}
	return nil
}

// dashboardBound measures a dashboard as the signing encoding carries it,
// which is the size a host stores, and names the defect when it is past the
// bound — or cannot be encoded at all, which no document decodes to and only a
// Go producer can build. "" when the dashboard is within it.
func dashboardBound(dashboard any) string {
	encoded, err := canonicalJSON(dashboard)
	switch {
	case err != nil:
		return "cannot be encoded: " + err.Error()
	case len(encoded) > FrontendDashboardMaxBytes:
		return fmt.Sprintf("encodes to %d bytes, more than the %d a dashboard may hold", len(encoded), FrontendDashboardMaxBytes)
	}
	return ""
}

func checkFrontendDashboardBounded(document *SolutionHostBinding) error {
	if document.Frontend.Dashboard == nil {
		return nil
	}
	if defect := dashboardBound(document.Frontend.Dashboard); defect != "" {
		return fmt.Errorf("%w: frontend.dashboard %s; a data graph declares events, metrics and widgets, and a host stores the whole generation", ErrInvalid, defect)
	}
	return nil
}

// boundedTexts includes identifiers and selectors as well as displayed text.
// Error messages name the field without echoing an oversized value.
func (frontend *Frontend) boundedTexts() []frontendText {
	texts := frontend.texts()
	for index, surface := range frontend.Surfaces {
		at := fmt.Sprintf("frontend.surfaces[%d]", index)
		texts = append(texts,
			frontendText{path: at + ".id", value: surface.ID},
			frontendText{path: at + ".client", value: surface.Client},
			frontendText{path: at + ".module", value: surface.Module})
		for i, event := range surface.Events {
			texts = append(texts, frontendText{path: fmt.Sprintf("%s.events[%d]", at, i), value: event})
		}
		if object, ok := surface.Applies.(map[string]any); ok {
			if tags, ok := object["tagged"].([]any); ok {
				for i, tag := range tags {
					if value, ok := tag.(string); ok {
						texts = append(texts, frontendText{path: fmt.Sprintf("%s.applies.tagged[%d]", at, i), value: value})
					}
				}
			}
		}
	}
	return texts
}

func checkFrontendTextBounded(document *SolutionHostBinding) error {
	for _, text := range document.Frontend.boundedTexts() {
		if len(text.value) > FrontendTextMaxBytes {
			return fmt.Errorf("%w: frontend text exceeds %d bytes at %s", ErrInvalid, FrontendTextMaxBytes, text.path)
		}
	}
	return nil
}

func frontendCollectionOverflow(frontend *Frontend) string {
	if len(frontend.Surfaces) > FrontendCollectionMaxItems {
		return "frontend.surfaces"
	}
	for index, surface := range frontend.Surfaces {
		if len(surface.Events) > FrontendCollectionMaxItems {
			return fmt.Sprintf("frontend.surfaces[%d].events", index)
		}
		if object, ok := surface.Applies.(map[string]any); ok {
			if tags, ok := object["tagged"].([]any); ok && len(tags) > FrontendCollectionMaxItems {
				return fmt.Sprintf("frontend.surfaces[%d].applies.tagged", index)
			}
		}
	}
	return ""
}

func checkFrontendCollectionsBounded(document *SolutionHostBinding) error {
	if path := frontendCollectionOverflow(document.Frontend); path != "" {
		return fmt.Errorf("%w: frontend collection exceeds %d items at %s", ErrInvalid, FrontendCollectionMaxItems, path)
	}
	return nil
}

func checkFrontendBounded(document *SolutionHostBinding) error {
	encoded, err := canonicalJSON(document.Frontend)
	if err != nil || len(encoded) > FrontendMaxBytes {
		return fmt.Errorf("%w: frontend must encode within %d bytes", ErrInvalid, FrontendMaxBytes)
	}
	return nil
}

// canonical returns a copy with the surfaces in the document's canonical
// order — by client, then id, the pair that identifies one — and each
// surface's events sorted, so a producer emitting either from a Go map
// cannot move the digest per process. The dashboard's object keys, and the
// tags a surface applies to, are sorted by the canonical encoding itself at
// every depth where they are keys; lists keep their order, as JSON lists do,
// and a tag list is one, since the client reads it in order.
func (frontend *Frontend) canonical() *Frontend {
	normalized := *frontend
	normalized.Surfaces = slices.Clone(frontend.Surfaces)
	for index, surface := range normalized.Surfaces {
		if surface.Events != nil {
			normalized.Surfaces[index].Events = slices.Sorted(slices.Values(surface.Events))
		}
	}
	sort.Slice(normalized.Surfaces, func(i, j int) bool {
		left, right := normalized.Surfaces[i], normalized.Surfaces[j]
		if left.Client != right.Client {
			return left.Client < right.Client
		}
		return left.ID < right.ID
	})
	return &normalized
}
