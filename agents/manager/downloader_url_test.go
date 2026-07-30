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
