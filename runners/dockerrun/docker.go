package dockerrun

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types"
)

// dockerProbeTimeout bounds backend discovery. Companion selection is a
// control-plane decision and must never inherit an unbounded command context:
// an unhealthy Docker socket would otherwise wedge every code generation,
// build, and test request instead of allowing the Nix/local fallback.
const dockerProbeTimeout = 5 * time.Second

type DockerPortMapping struct {
	Host      uint16
	Container uint16
}

func DockerEngineRunning(ctx context.Context) bool {
	cli, _, err := newDockerClient()
	if err != nil {
		return false
	}
	defer cli.Close()
	return pingDockerClient(ctx, cli) == nil
}

func pingDockerClient(ctx context.Context, cli interface {
	Ping(context.Context) (types.Ping, error)
}) error {
	probeCtx, cancel := context.WithTimeout(ctx, dockerProbeTimeout)
	defer cancel()
	_, err := cli.Ping(probeCtx)
	return err
}

type ProgressDetail struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type DockerPullResponse struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	ProgressDetail ProgressDetail `json:"progressDetail"`
	Error          string         `json:"error"`
	ErrorDetail    struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func PrintDownloadPercentage(reader io.ReadCloser, out io.Writer) error {
	// Own the reader's lifetime here — callers pass it in pre-opened from
	// ImagePull and were relying on us to drain it. Forgetting this close
	// leaked one FD per image pull (the audit caught it).
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Split(bufio.ScanLines)

	progressMap := make(map[string]DockerPullResponse)
	var progressMu sync.Mutex

	ticker := time.NewTicker(5 * time.Second)
	done := make(chan struct{})
	var reporter sync.WaitGroup
	reporter.Add(1)
	go func() {
		defer reporter.Done()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var totalCurrent int
				progressMu.Lock()
				for _, progress := range progressMap {
					totalCurrent += progress.ProgressDetail.Current
				}
				progressMu.Unlock()
				totalCurrentMB := float64(totalCurrent) / 1024 / 1024
				_, _ = out.Write([]byte(fmt.Sprintf("Downloaded: %.2f MB", totalCurrentMB)))
			}
		}
	}()
	defer func() {
		ticker.Stop()
		close(done)
		reporter.Wait()
	}()

	for scanner.Scan() {
		line := scanner.Text()
		var pullResponse DockerPullResponse
		if err := json.Unmarshal([]byte(line), &pullResponse); err != nil {
			return fmt.Errorf("decode Docker pull progress: %w", err)
		}
		pullError := strings.TrimSpace(pullResponse.ErrorDetail.Message)
		if pullError == "" {
			pullError = strings.TrimSpace(pullResponse.Error)
		}
		if pullError != "" {
			return fmt.Errorf("Docker registry reported: %s", pullError)
		}
		if pullResponse.ID != "" {
			progressMu.Lock()
			progressMap[pullResponse.ID] = pullResponse
			progressMu.Unlock()
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Docker pull progress: %w", err)
	}
	return nil
}

type DockerContainerInstance struct {
	ID string
}

func GetImageID(im *resources.DockerImage) (string, error) {
	ctx := context.Background()

	cli, _, err := newDockerClient()
	if err != nil {
		return "", fmt.Errorf("failed to create Docker client: %v", err)
	}
	defer cli.Close()

	// Pull the image if necessary

	// Inspect the image

	inspect, _, err := cli.ImageInspectWithRaw(ctx, im.FullName())
	if err != nil {
		return "", fmt.Errorf("failed to inspect im: %v", err)
	}

	return inspect.ID, nil
}
