package dockerrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/runners/rust"
	"github.com/stretchr/testify/require"
)

func TestDestroyRuntimeAcquiresContainerForTeardown(t *testing.T) {
	for _, runtime := range []struct {
		name    string
		destroy func(context.Context, *basev0.RuntimeContext, *resources.DockerImage, string, string, string, string) error
	}{
		{"goland", golang.DestroyGoRuntime},
		{"rust", rust.DestroyRustRuntime},
	} {
		t.Run(runtime.name, func(t *testing.T) {
			for _, state := range []string{"present", "cancelled caller", "replaced after acquisition", "missing", "inspect fails", "unowned", "wrong name label", "empty ID", "stop fails", "remove fails"} {
				t.Run(state, func(t *testing.T) {
					name := dockerrun.ContainerName(runtime.name + "-review")
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						path := strings.TrimPrefix(r.URL.Path, "/v1.47")
						requests = append(requests, r.Method+" "+path)
						w.Header().Set("Content-Type", "application/json")
						switch {
						case r.Method == http.MethodGet && path == "/containers/"+name+"/json":
							if state == "missing" || state == "inspect fails" {
								status := http.StatusNotFound
								if state == "inspect fails" {
									status = http.StatusInternalServerError
								}
								w.WriteHeader(status)
								_, _ = fmt.Fprint(w, `{"message":"inspect failed"}`)
								return
							}
							labels := map[string]string{dockerrun.LabelCodeflyOwner: "true", dockerrun.LabelCodeflyName: name}
							if state == "unowned" {
								labels = nil
							}
							if state == "wrong name label" {
								labels[dockerrun.LabelCodeflyName] = "codefly-other"
							}
							id := "acquired"
							if state == "empty ID" {
								id = ""
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{"Labels": labels}})
						case r.Method == http.MethodPost && path == "/containers/acquired/stop",
							r.Method == http.MethodDelete && path == "/containers/acquired":
							status := http.StatusNoContent
							if state == "replaced after acquisition" {
								status = http.StatusNotFound
							}
							if state == "stop fails" && r.Method == http.MethodPost || state == "remove fails" && r.Method == http.MethodDelete {
								status = http.StatusInternalServerError
							}
							w.WriteHeader(status)
							if status != http.StatusNoContent {
								_, _ = fmt.Fprint(w, `{"message":"teardown failed"}`)
							}
						default:
							t.Errorf("unexpected Docker operation %s %s", r.Method, path)
							w.WriteHeader(http.StatusInternalServerError)
						}
					}))
					defer server.Close()
					t.Setenv("DOCKER_HOST", server.URL)
					t.Setenv("DOCKER_API_VERSION", "1.47")
					t.Setenv("DOCKER_TLS_VERIFY", "")
					t.Setenv("DOCKER_CERT_PATH", "")
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if state == "cancelled caller" {
						cancel() // Teardown must still run after the caller has cancelled.
					}
					err := runtime.destroy(ctx, resources.NewRuntimeContextContainer(), resources.NewDockerImage("review:latest"), t.TempDir(), t.TempDir(), "", "review")
					if state == "inspect fails" || state == "unowned" || state == "wrong name label" || state == "empty ID" || state == "remove fails" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					expected := []string{"GET /containers/" + name + "/json"}
					if state == "present" || state == "cancelled caller" || state == "replaced after acquisition" || state == "stop fails" || state == "remove fails" {
						expected = append(expected, "POST /containers/acquired/stop", "DELETE /containers/acquired")
					}
					require.Equal(t, expected, requests)
				})
			}
		})
	}
}
