package network

import (
	"net"
	"strconv"
	"testing"
)

func TestKernelTemporaryPortProbeOwnsLoopbackPort(t *testing.T) {
	listener, allocatedPort, err := listenTemporaryPort()
	if err != nil {
		t.Fatalf("bind kernel-assigned loopback port: %v", err)
	}
	defer listener.Close()

	port := int(allocatedPort)
	if port == 0 {
		t.Fatal("kernel returned port zero")
	}

	second, err := net.Listen("tcp4", net.JoinHostPort(Localhost, strconv.Itoa(port)))
	if err == nil {
		second.Close()
		t.Fatalf("kernel-assigned port %d was not exclusively reserved by the probe listener", port)
	}
}
