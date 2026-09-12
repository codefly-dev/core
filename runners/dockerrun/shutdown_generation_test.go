package dockerrun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
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

func TestShutdownAfterFailedStartupRollback(t *testing.T) {
	for _, state := range []string{"rollback succeeds", "rollback not found", "rollback fails transiently", "removal keeps failing", "replaced after rollback"} {
		t.Run(state, func(t *testing.T) {
			var requests []string
			removes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				requests = append(requests, r.Method+" "+path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && path == "/containers/create":
					w.WriteHeader(http.StatusCreated)
					_, _ = fmt.Fprint(w, `{"Id":"acquired"}`)
				case r.Method == http.MethodPost && path == "/containers/acquired/start":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodGet && path == "/containers/acquired/json":
					// Docker started the container, but observing its state failed.
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprint(w, `{"message":"temporary inspect failure"}`)
				case r.Method == http.MethodDelete && path == "/containers/acquired":
					removes++
					status := http.StatusNoContent
					if state == "rollback not found" || state == "replaced after rollback" && removes > 1 {
						status = http.StatusNotFound
					} else if state == "removal keeps failing" || removes == 1 && state != "rollback succeeds" {
						status = http.StatusInternalServerError
					}
					w.WriteHeader(status)
					if status != http.StatusNoContent {
						_, _ = fmt.Fprint(w, `{"message":"remove failed"}`)
					}
				case r.Method == http.MethodPost && path == "/containers/acquired/stop":
					if state == "replaced after rollback" {
						w.WriteHeader(http.StatusNotFound)
						_, _ = fmt.Fprint(w, `{"message":"gone"}`)
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
			defer cli.Close()
			env := &DockerEnvironment{client: cli, name: "codefly-review"}
			require.Error(t, env.createAndStartContainer(context.Background(), &container.Config{Image: "review"}, &container.HostConfig{}))
			expected := []string{"POST /containers/create", "POST /containers/acquired/start", "GET /containers/acquired/json", "DELETE /containers/acquired"}
			if state == "rollback succeeds" || state == "rollback not found" {
				_, err = env.ContainerID()
				require.Error(t, err)
			} else {
				id, idErr := env.ContainerID()
				require.NoError(t, idErr)
				require.Equal(t, "acquired", id)
				expected = append(expected, "POST /containers/acquired/stop", "DELETE /containers/acquired")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = env.Shutdown(ctx)
			if state == "removal keeps failing" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, expected, requests)
		})
	}
}
