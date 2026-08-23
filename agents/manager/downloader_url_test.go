package manager

import (
	"runtime"
	"testing"

	"github.com/codefly-dev/core/resources"
)

func TestDownloadURLUsesAgentPublisherAndCurrentPlatform(t *testing.T) {
	agent := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.com", Name: "widget", Version: "1.2.3"}
	want := "https://github.com/example-com/service-widget/releases/download/v1.2.3/" +
		"service-widget_1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	got, err := DownloadURL(agent)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDownloadURLResolvesLinuxARM64ServiceAgent pins core's side of the
// multi-arch contract: on a linux/arm64 host, core requests exactly the
// service-<name>_<version>_linux_arm64.tar.gz asset the service-* release
// matrices must publish. No CI host builds on linux/arm64, so asserting the
// running platform alone would never exercise this path.
func TestDownloadURLResolvesLinuxARM64ServiceAgent(t *testing.T) {
	agent := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "go", Version: "0.0.33"}
	want := "https://github.com/codefly-dev/service-go/releases/download/v0.0.33/" +
		"service-go_0.0.33_linux_arm64.tar.gz"
	got, err := downloadURLForPlatform(agent, "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
