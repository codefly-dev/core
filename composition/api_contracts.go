package composition

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/codefly-dev/core/standards"
	"google.golang.org/protobuf/types/descriptorpb"
)

type APIContractCatalog struct {
	Schema    string                `json:"schema"`
	Package   string                `json:"package"` // manifest ID, e.g. codefly/saas-starter
	Version   string                `json:"version"` // manifest Version
	Endpoints []APIContractEndpoint `json:"endpoints"`
}

type APIContractEndpoint struct {
	Service  string               `json:"service"`
	Endpoint string               `json:"endpoint"`
	API      string               `json:"api"`  // grpc | rest | connect | http (standards.APIS())
	Kind     string               `json:"kind"` // protobuf | openapi
	Package  string               `json:"package"`
	Path     string               `json:"path"` // same as ProvidedAPIContract.Path
	Digest   string               `json:"digest"`
	Services []APIContractService `json:"services,omitempty"` // protobuf only
	Routes   []APIContractRoute   `json:"routes,omitempty"`   // openapi only
}

type APIContractService struct {
	Name       string   `json:"name"`       // AuditService
	FullName   string   `json:"fullName"`   // saas.accounts.v1.AuditService
	Procedures []string `json:"procedures"` // /saas.accounts.v1.AuditService/QueryAuditLog
}

type APIContractRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func LoadAPIContractCatalog(moduleDir string) (*APIContractCatalog, error) {
	data, err := os.ReadFile(filepath.Join(moduleDir, filepath.FromSlash(APIContractCatalogFileName)))
	if err != nil {
		return nil, fmt.Errorf("read API contract catalog: %w", err)
	}
	var catalog APIContractCatalog
	if err := decodeStrictJSON(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode API contract catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (c *APIContractCatalog) Validate() error {
	if c == nil {
		return fmt.Errorf("API contract catalog is required")
	}
	if c.Schema != APIContractCatalogSchema {
		return fmt.Errorf("API contract catalog schema must be %q", APIContractCatalogSchema)
	}
	if err := validateIdentifier("API contract catalog package", c.Package); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(c.Version); err != nil {
		return fmt.Errorf("API contract catalog version %q is invalid: %w", c.Version, err)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for _, endpoint := range c.Endpoints {
		key := endpoint.Service + "\x00" + endpoint.Endpoint
		if _, exists := seen[key]; exists {
			return fmt.Errorf("API contract catalog endpoint %s/%s is declared twice", endpoint.Service, endpoint.Endpoint)
		}
		seen[key] = struct{}{}
		if err := endpoint.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (endpoint APIContractEndpoint) validate() error {
	label := fmt.Sprintf("API contract endpoint %s/%s", endpoint.Service, endpoint.Endpoint)
	if strings.TrimSpace(endpoint.Service) == "" || strings.TrimSpace(endpoint.Endpoint) == "" {
		return fmt.Errorf("API contract endpoint requires a service and an endpoint")
	}
	if !slices.Contains(standards.APIS(), endpoint.API) {
		return fmt.Errorf("%s api %q is not supported", label, endpoint.API)
	}
	if endpoint.Kind != APIContractKindProtobuf && endpoint.Kind != APIContractKindOpenAPI {
		return fmt.Errorf("%s kind %q must be %q or %q", label, endpoint.Kind, APIContractKindProtobuf, APIContractKindOpenAPI)
	}
	if strings.TrimSpace(endpoint.Package) == "" {
		return fmt.Errorf("%s package is required", label)
	}
	if err := validateContractPath(label, endpoint.Path); err != nil {
		return err
	}
	if !digestPattern.MatchString(endpoint.Digest) {
		return fmt.Errorf("%s digest %q must be a SHA-256 digest", label, endpoint.Digest)
	}
	switch endpoint.Kind {
	case APIContractKindProtobuf:
		if len(endpoint.Services) == 0 {
			return fmt.Errorf("%s protobuf contract must declare at least one service", label)
		}
		fullNames := make(map[string]struct{}, len(endpoint.Services))
		for _, service := range endpoint.Services {
			if _, exists := fullNames[service.FullName]; exists {
				return fmt.Errorf("%s declares service %q twice", label, service.FullName)
			}
			fullNames[service.FullName] = struct{}{}
			for _, procedure := range service.Procedures {
				prefix := "/" + service.FullName + "/"
				method := strings.TrimPrefix(procedure, prefix)
				if !strings.HasPrefix(procedure, prefix) || method == "" || strings.Contains(method, "/") {
					return fmt.Errorf("%s procedure %q must be %q + method", label, procedure, prefix)
				}
			}
		}
		if len(endpoint.Routes) != 0 {
			return fmt.Errorf("%s protobuf contract must not declare routes", label)
		}
	case APIContractKindOpenAPI:
		if len(endpoint.Routes) == 0 {
			return fmt.Errorf("%s openapi contract must declare at least one route", label)
		}
		if len(endpoint.Services) != 0 {
			return fmt.Errorf("%s openapi contract must not declare services", label)
		}
	}
	return nil
}

func (c *APIContractCatalog) CanonicalBytes() ([]byte, error) {
	canonical := APIContractCatalog{
		Schema:    c.Schema,
		Package:   c.Package,
		Version:   c.Version,
		Endpoints: make([]APIContractEndpoint, len(c.Endpoints)),
	}
	copy(canonical.Endpoints, c.Endpoints)
	sort.Slice(canonical.Endpoints, func(i, j int) bool {
		if canonical.Endpoints[i].Service != canonical.Endpoints[j].Service {
			return canonical.Endpoints[i].Service < canonical.Endpoints[j].Service
		}
		return canonical.Endpoints[i].Endpoint < canonical.Endpoints[j].Endpoint
	})
	for i := range canonical.Endpoints {
		endpoint := &canonical.Endpoints[i]
		services := make([]APIContractService, len(endpoint.Services))
		copy(services, endpoint.Services)
		for j := range services {
			procedures := slices.Clone(services[j].Procedures)
			sort.Strings(procedures)
			services[j].Procedures = procedures
		}
		sort.Slice(services, func(a, b int) bool { return services[a].FullName < services[b].FullName })
		endpoint.Services = services
		routes := slices.Clone(endpoint.Routes)
		sort.Slice(routes, func(a, b int) bool {
			if routes[a].Path != routes[b].Path {
				return routes[a].Path < routes[b].Path
			}
			return routes[a].Method < routes[b].Method
		})
		endpoint.Routes = routes
	}
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func APIContractDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func ValidatePackageAPIContracts(moduleDir string, manifest *PackageManifest, catalog *APIContractCatalog) error {
	// The catalog may be supplied by a caller that built it in memory (e.g. the
	// CLI from a descriptor set) rather than through LoadAPIContractCatalog, so
	// this verifier cannot assume it is already valid. Re-validate before any of
	// its fields — in particular the file Path this function reads from disk —
	// are trusted.
	if err := catalog.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrContract, err)
	}
	catalogByEndpoint := make(map[string]APIContractEndpoint, len(catalog.Endpoints))
	for _, endpoint := range catalog.Endpoints {
		key := endpoint.Service + "\x00" + endpoint.Endpoint
		catalogByEndpoint[key] = endpoint
	}
	declared := make(map[string]struct{})
	for _, service := range manifest.Services {
		for _, contract := range service.APIContracts {
			key := service.Name + "\x00" + contract.Endpoint
			declared[key] = struct{}{}
			endpoint, exists := catalogByEndpoint[key]
			if !exists {
				return fmt.Errorf("%w: manifest declares contract for %s/%s with no catalog entry", ErrContract, service.Name, contract.Endpoint)
			}
			if endpoint.Path != contract.Path || endpoint.Kind != contract.Kind || endpoint.Package != contract.Package || endpoint.Digest != contract.Digest {
				return fmt.Errorf("%w: manifest and catalog disagree on contract for %s/%s", ErrContract, service.Name, contract.Endpoint)
			}
		}
	}
	for _, endpoint := range catalog.Endpoints {
		key := endpoint.Service + "\x00" + endpoint.Endpoint
		if _, exists := declared[key]; !exists {
			return fmt.Errorf("%w: catalog declares contract for %s/%s not present in the manifest", ErrContract, endpoint.Service, endpoint.Endpoint)
		}
		if !pathUnderArtifactRoots(endpoint.Path, manifest.ArtifactRoots) {
			return fmt.Errorf("%w: contract path %q for %s/%s is not under an artifact root", ErrContract, endpoint.Path, endpoint.Service, endpoint.Endpoint)
		}
		data, err := os.ReadFile(filepath.Join(moduleDir, filepath.FromSlash(endpoint.Path)))
		if err != nil {
			return fmt.Errorf("%w: read contract file %q: %v", ErrContract, endpoint.Path, err)
		}
		if digest := APIContractDigest(data); digest != endpoint.Digest {
			return fmt.Errorf("%w: contract file %q digest %s does not match catalog digest %s", ErrContract, endpoint.Path, digest, endpoint.Digest)
		}
	}
	return nil
}

// ProtobufServices walks set.File for files in pkg and returns their services
// with their fully qualified procedures, sorted by full name and procedure.
func ProtobufServices(set *descriptorpb.FileDescriptorSet, pkg string) []APIContractService {
	var services []APIContractService
	for _, file := range set.GetFile() {
		if file.GetPackage() != pkg {
			continue
		}
		for _, service := range file.GetService() {
			fullName := pkg + "." + service.GetName()
			procedures := make([]string, 0, len(service.GetMethod()))
			for _, method := range service.GetMethod() {
				procedures = append(procedures, "/"+fullName+"/"+method.GetName())
			}
			sort.Strings(procedures)
			services = append(services, APIContractService{
				Name:       service.GetName(),
				FullName:   fullName,
				Procedures: procedures,
			})
		}
	}
	sort.Slice(services, func(i, j int) bool { return services[i].FullName < services[j].FullName })
	return services
}

func validateContractPath(label, value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") {
		return fmt.Errorf("%s path %q is unsafe", label, value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%s path %q is unsafe", label, value)
		}
	}
	return nil
}

func pathUnderArtifactRoots(value string, roots []string) bool {
	// A ".." segment lets a raw prefix match ("contracts/../../secret" starts
	// with "contracts/") escape the artifact root once the path is joined and
	// cleaned, so containment must reject traversal itself rather than trust the
	// caller to have pre-validated the path.
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	for _, root := range roots {
		if value == root || strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}
