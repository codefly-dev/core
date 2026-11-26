package dockerrun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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
			cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.47"))
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
			cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.47"))
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

func TestShutdownJoinsConcurrentRemovalOfAcquiredGeneration(t *testing.T) {
	for _, state := range []string{"removing", "gone before inspect", "gone before wait", "unrelated conflict", "wait failed"} {
		t.Run(state, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				requests = append(requests, r.Method+" "+path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && path == "/containers/acquired/stop":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodDelete && path == "/containers/acquired":
					w.WriteHeader(http.StatusConflict)
					fmt.Fprint(w, `{"message":"operation conflicts"}`)
				case r.Method == http.MethodGet && path == "/containers/acquired/json":
					if state == "gone before inspect" {
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"message":"gone"}`)
						return
					}
					status := "removing"
					if state == "unrelated conflict" {
						status = "running"
					}
					fmt.Fprintf(w, `{"Id":"acquired","State":{"Status":%q}}`, status)
				case r.Method == http.MethodPost && path == "/containers/acquired/wait":
					if r.URL.Query().Get("condition") != "removed" {
						t.Error("wait did not require removal")
					}
					if state == "gone before wait" {
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"message":"gone"}`)
						return
					}
					if state == "wait failed" {
						fmt.Fprint(w, `{"StatusCode":-1,"Error":{"Message":"removal failed"}}`)
						return
					}
					fmt.Fprint(w, `{"StatusCode":0}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.47"))
			require.NoError(t, err)
			env := &DockerEnvironment{client: cli, instance: &DockerContainerInstance{ID: "acquired"}}
			err = env.Shutdown(context.Background())
			if state == "unrelated conflict" || state == "wait failed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			expected := []string{"POST /containers/acquired/stop", "DELETE /containers/acquired", "GET /containers/acquired/json"}
			if state != "gone before inspect" && state != "unrelated conflict" {
				expected = append(expected, "POST /containers/acquired/wait")
			}
			require.Equal(t, expected, requests)
		})
	}
}

func TestContainerCleanupContextKeepsLiveDeadlineAndBoundsFallback(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	parent, cancel := context.WithDeadline(context.Background(), deadline)
	ctx, release := containerCleanupContext(parent)
	got, ok := ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, deadline, got)
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	release()

	for _, parent := range []context.Context{parent, context.Background()} {
		before := time.Now()
		ctx, release := containerCleanupContext(parent)
		require.NoError(t, ctx.Err())
		got, ok := ctx.Deadline()
		require.True(t, ok)
		require.False(t, got.Before(before.Add(10*time.Second)))
		require.False(t, got.After(time.Now().Add(10*time.Second)))
		release()
	}
}

func TestShutdownKeepsTimeForRemoveWhenGracefulStopStalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	removed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			<-r.Context().Done()
		case http.MethodDelete:
			if ctx.Err() != nil {
				t.Error("graceful stop exhausted the caller deadline before removal")
			}
			removed <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Docker operation %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.47"))
	require.NoError(t, err)
	env := &DockerEnvironment{client: cli, instance: &DockerContainerInstance{ID: "acquired"}}
	require.NoError(t, env.Shutdown(ctx))
	require.Len(t, removed, 1)
}
