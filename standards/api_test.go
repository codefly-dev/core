package standards

import "testing"

func TestIsSupportedAPI(t *testing.T) {
	for _, api := range []string{GRPC, REST, HTTP, TCP, CONNECT} {
		if err := IsSupportedAPI(api); err != nil {
			t.Errorf("IsSupportedAPI(%s) errored: %v", api, err)
		}
	}
	if err := IsSupportedAPI("graphql"); err == nil {
		t.Error("graphql should not be supported")
	}
	if err := IsSupportedAPI(""); err == nil {
		t.Error("empty should not be supported")
	}
}

func TestPort_KnownAPIs(t *testing.T) {
	cases := map[string]uint16{
		GRPC:    9090,
		REST:    8080,
		HTTP:    8080,
		CONNECT: 8081,
		TCP:     80,
		"weird": 80, // default fallback
	}
	for api, want := range cases {
		if got := Port(api); got != want {
			t.Errorf("Port(%s) = %d, want %d", api, got, want)
		}
	}
}

func TestPortAddressForEndpoint(t *testing.T) {
	if got := PortAddressForEndpoint("user.grpc"); got != ":9090" {
		t.Errorf("grpc suffix: got %q", got)
	}
	if got := PortAddressForEndpoint("user.rest"); got != ":8080" {
		t.Errorf("rest suffix: got %q", got)
	}
	if got := PortAddressForEndpoint("anything-else"); got != ":80" {
		t.Errorf("default: got %q", got)
	}
}

func TestAPIS_Contents(t *testing.T) {
	apis := APIS()
	have := map[string]bool{}
	for _, a := range apis {
		have[a] = true
	}
	for _, want := range []string{GRPC, REST, HTTP, TCP, CONNECT} {
		if !have[want] {
			t.Errorf("APIS missing %s", want)
		}
	}
}
