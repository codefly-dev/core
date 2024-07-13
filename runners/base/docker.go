package base

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/client"
)

type DockerPortMapping struct {
	Host      uint16
	Container uint16
}

func DockerEngineRunning(ctx context.Context) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	_, err = cli.Ping(ctx)
	return err == nil
}

type ProgressDetail struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type DockerPullResponse struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	ProgressDetail ProgressDetail `json:"progressDetail"`
}

func PrintDownloadPercentage(reader io.ReadCloser, out io.Writer) {
	scanner := bufio.NewScanner(reader)
	scanner.Split(bufio.ScanLines)

	progressMap := make(map[string]DockerPullResponse)

	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			var totalCurrent int
			for _, progress := range progressMap {
				totalCurrent += progress.ProgressDetail.Current
			}
			totalCurrentMB := float64(totalCurrent) / 1024 / 1024
			_, _ = out.Write([]byte(fmt.Sprintf("Downloaded: %.2f MB", totalCurrentMB)))
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()
		var pullResponse DockerPullResponse
		_ = json.Unmarshal([]byte(line), &pullResponse)
		progressMap[pullResponse.ID] = pullResponse
	}

	ticker.Stop()
}

type DockerContainerInstance struct {
	ID string
}

func GetImageID(im *resources.DockerImage) (string, error) {
	ctx := context.Background()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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
