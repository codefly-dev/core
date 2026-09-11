package dockerrun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

// The daemon has a successor at the old name; a name lookup would select it.
// Only the originally acquired immutable ID may be stopped or removed.
func TestShutdownUsesAcquiredGeneration(t *testing.T) {
	for _, state := range []string{"present", "replaced", "never acquired", "remove fails"} {
		t.Run(state, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				requests = append(requests, r.Method+" "+path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case path == "/containers/json":
					_, _ = fmt.Fprint(w, `[{"Id":"successor","Names":["/codefly-store"]}]`)
				case path == "/containers/acquired/stop" || path == "/containers/acquired":
					if state == "replaced" {
						w.WriteHeader(http.StatusNotFound)
						_, _ = fmt.Fprint(w, `{"message":"gone"}`)
					} else if state == "remove fails" && r.Method == http.MethodDelete {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = fmt.Fprint(w, `{"message":"cannot remove"}`)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Errorf("unexpected Docker operation %s %s", r.Method, path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			require.NoError(t, err)
			env := &DockerEnvironment{client: cli, name: "codefly-store"}
			if state != "never acquired" {
				env.instance = &DockerContainerInstance{ID: "acquired"}
			}
			err = env.Shutdown(context.Background())
			if state == "remove fails" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if state == "never acquired" {
				require.Empty(t, requests)
			} else {
				require.Equal(t, []string{"POST /containers/acquired/stop", "DELETE /containers/acquired"}, requests)
			}
		})
	}
}
