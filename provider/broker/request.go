package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/manifest"
)

var methodNames = map[providerv0.HTTPMethod]string{
	providerv0.HTTPMethod_HTTP_METHOD_GET:    http.MethodGet,
	providerv0.HTTPMethod_HTTP_METHOD_HEAD:   http.MethodHead,
	providerv0.HTTPMethod_HTTP_METHOD_POST:   http.MethodPost,
	providerv0.HTTPMethod_HTTP_METHOD_PUT:    http.MethodPut,
	providerv0.HTTPMethod_HTTP_METHOD_PATCH:  http.MethodPatch,
	providerv0.HTTPMethod_HTTP_METHOD_DELETE: http.MethodDelete,
}

// buildRequest constructs the exact outbound request. The provider supplies
// only descriptor-allowed structured fields; the host owns the URL, every
// security-relevant header, and the encoding. Path parameters and ownership
// body fields are bound to the action's planned remote id, so a valid template
// carrying another id fails before any credential is resolved.
func (s *Session) buildRequest(
	ctx context.Context,
	descriptor manifest.RequestDescriptor,
	planned *providerv0.PlannedRequest,
	origin urlguard.Origin,
	remoteID string,
) (*http.Request, int64, error) {
	method, ok := methodNames[planned.GetMethod()]
	if !ok || method != descriptor.Method {
		return nil, 0, fmt.Errorf("request method does not match descriptor")
	}

	path, err := s.bindPath(descriptor, planned, remoteID)
	if err != nil {
		return nil, 0, err
	}
	query, err := bindQuery(descriptor, planned)
	if err != nil {
		return nil, 0, err
	}
	body, size, err := bindBody(descriptor, planned, remoteID)
	if err != nil {
		return nil, 0, err
	}

	target := url.URL{Scheme: origin.Scheme, Host: origin.HostPort(), Path: path, RawQuery: query}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	s.applyOwnedHeaders(request, planned, len(body) > 0)
	return request, int64(size), nil
}

func (s *Session) bindPath(descriptor manifest.RequestDescriptor, planned *providerv0.PlannedRequest, remoteID string) (string, error) {
	template := descriptor.PathTemplate
	params := planned.GetPathParameters()
	if len(params) != len(descriptor.RemoteIDParameters) {
		return "", fmt.Errorf("path parameters do not match descriptor")
	}
	for _, name := range descriptor.RemoteIDParameters {
		value, ok := params[name]
		if !ok {
			return "", fmt.Errorf("path parameter %q is missing", name)
		}
		scalar, err := scalarString(value)
		if err != nil {
			return "", fmt.Errorf("path parameter %q: %w", name, err)
		}
		// Every remote-id path parameter must equal the action's planned id.
		if scalar != remoteID {
			return "", fmt.Errorf("path parameter %q is not the planned remote id", name)
		}
		template = strings.ReplaceAll(template, "{"+name+"}", url.PathEscape(scalar))
	}
	if strings.ContainsAny(template, "{}") {
		return "", fmt.Errorf("path template has unbound placeholders")
	}
	return urlguard.SafePath(template)
}

func bindQuery(descriptor manifest.RequestDescriptor, planned *providerv0.PlannedRequest) (string, error) {
	values := url.Values{}
	for key, value := range planned.GetQuery() {
		if !slices.Contains(descriptor.AllowedQueryFields, key) {
			return "", fmt.Errorf("query field %q is not allowed", key)
		}
		scalar, err := scalarString(value)
		if err != nil {
			return "", fmt.Errorf("query field %q: %w", key, err)
		}
		values.Set(key, scalar)
	}
	return values.Encode(), nil
}

func bindBody(descriptor manifest.RequestDescriptor, planned *providerv0.PlannedRequest, remoteID string) ([]byte, int, error) {
	if len(planned.GetBody()) == 0 {
		return nil, 0, nil
	}
	object := make(map[string]any, len(planned.GetBody()))
	for key, value := range planned.GetBody() {
		if !slices.Contains(descriptor.AllowedBodyFields, key) {
			return nil, 0, fmt.Errorf("body field %q is not allowed", key)
		}
		if slices.Contains(descriptor.OwnershipBodyFields, key) {
			scalar, err := scalarString(value)
			if err != nil || scalar != remoteID {
				return nil, 0, fmt.Errorf("ownership body field %q is not the planned remote id", key)
			}
		}
		converted, err := nativeValue(value)
		if err != nil {
			return nil, 0, fmt.Errorf("body field %q: %w", key, err)
		}
		object[key] = converted
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, 0, err
	}
	return encoded, len(encoded), nil
}

// applyOwnedHeaders installs the host-controlled header set. The provider
// contributes no headers at all: there is no caller-header allowlist to widen
// because the descriptor carries structured fields only. Authorization is left
// for the credential vault to inject.
func (s *Session) applyOwnedHeaders(request *http.Request, planned *providerv0.PlannedRequest, hasBody bool) {
	request.Header = http.Header{}
	request.Host = request.URL.Host
	request.Header.Set("User-Agent", s.userAgent)
	request.Header.Set("Accept", "application/json")
	// The host owns decompression; it never lets a caller pick the encoding.
	request.Header.Set("Accept-Encoding", "identity")
	if hasBody {
		request.Header.Set("Content-Type", "application/json")
	}
	if key := planned.GetIdempotencyKey(); key != "" && isMutating(planned.GetMethod()) {
		request.Header.Set("Idempotency-Key", key)
	}
}

func isMutating(method providerv0.HTTPMethod) bool {
	switch method {
	case providerv0.HTTPMethod_HTTP_METHOD_GET, providerv0.HTTPMethod_HTTP_METHOD_HEAD:
		return false
	default:
		return true
	}
}
