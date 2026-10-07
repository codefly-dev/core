package otel_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/codefly-dev/core/wool"
	wooltel "github.com/codefly-dev/core/wool/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

// These tests answer one question about the OTLP/gRPC trace exporter: what does
// the first thing it writes to the collector's socket look like? A plaintext
// gRPC client opens with the HTTP/2 connection preface ("PRI * HTTP/2.0 ..."); a
// TLS client opens with a ClientHello (record type 0x16). Reading those bytes off
// a real TCP listener says which transport Enable actually built, with no fake
// exporter in between and no dependence on which roots the machine trusts.

// dial is what the collector's listener saw from the exporter's first connection.
type dial struct {
	plaintext bool // opened with the HTTP/2 preface
	tls       bool // opened with a TLS record
	// handshakeErr is the server side of a TLS handshake that was attempted
	// against a certificate no system root vouches for. It is nil for a
	// plaintext dial.
	handshakeErr error
}

// peekedConn lets a TLS server read back the bytes the sniffer already looked at.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// selfSignedCert is a certificate for 127.0.0.1 that no system root signed.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "collector.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// sniffingCollector listens on a loopback port and reports how each connection
// to it opened. It never answers: the exporter's export fails, which is fine,
// because the opening bytes are the whole observation.
func sniffingCollector(t *testing.T) (addr string, dials <-chan dial) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	serverTLS := &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}, NextProtos: []string{"h2"}}
	out := make(chan dial, 16)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				head, err := reader.Peek(4)
				if err != nil {
					return
				}
				var seen dial
				switch {
				case string(head) == "PRI ":
					seen.plaintext = true
				case head[0] == 0x16: // TLS handshake record
					seen.tls = true
					seen.handshakeErr = tls.Server(&peekedConn{Conn: conn, r: reader}, serverTLS).Handshake()
				}
				select {
				case out <- seen:
				default:
				}
			}()
		}
	}()
	return listener.Addr().String(), out
}

// enableAndExport enables the real backend with opts, ends one span so the
// exporter has something to send, and returns what the collector first saw.
// Enable replaces OTEL's tracer provider and wool's telemetry provider; both are
// restored, and the tests stay serial because that state is process-wide.
func enableAndExport(t *testing.T, dials <-chan dial, opts ...wooltel.Option) dial {
	t.Helper()
	previousTracerProvider := otel.GetTracerProvider()
	previousTelemetry := wool.GetTelemetry()
	backend, err := wooltel.Enable(opts...)
	require.NoError(t, err)

	// Shutdown flushes the batcher, which is what makes the exporter dial. It
	// runs in the background so the test can stop it as soon as the dial has been
	// observed instead of waiting out the export's retries.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-done
		otel.SetTracerProvider(previousTracerProvider)
		wool.RegisterTelemetry(previousTelemetry)
	})

	_, span := backend.NewTracer("endpoint-url-test").Start(context.Background(), "export")
	span.End()
	go func() {
		defer close(done)
		_ = backend.Shutdown(ctx)
	}()

	select {
	case first := <-dials:
		return first
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the exporter never connected to the collector")
		return dial{}
	}
}

// requirePlaintext and requireTLS name the two outcomes the tests choose between.
func requirePlaintext(t *testing.T, got dial) {
	t.Helper()
	require.True(t, got.plaintext, "want an HTTP/2 preface in the clear, got %+v", got)
	require.False(t, got.tls)
}

func requireTLS(t *testing.T, got dial) {
	t.Helper()
	require.True(t, got.tls, "want a TLS ClientHello, got %+v", got)
	require.False(t, got.plaintext)
}

func TestWithEndpointURL_SchemeChoosesTransport(t *testing.T) {
	t.Run("http is plaintext", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requirePlaintext(t, enableAndExport(t, dials,
			wooltel.WithEndpointURL("http://"+addr), wooltel.WithServiceName("svc")))
	})

	t.Run("https is TLS", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials,
			wooltel.WithEndpointURL("https://"+addr), wooltel.WithServiceName("svc")))
	})

	t.Run("https verifies the collector against the system roots", func(t *testing.T) {
		// The collector's certificate is self-signed, so no system root vouches
		// for it. A client that verified would refuse it, and the server side of
		// the handshake sees that refusal. A client that skipped verification
		// would complete the handshake.
		addr, dials := sniffingCollector(t)
		got := enableAndExport(t, dials, wooltel.WithEndpointURL("https://"+addr))
		requireTLS(t, got)
		require.Error(t, got.handshakeErr, "the exporter must not accept an untrusted certificate")
		require.ErrorContains(t, got.handshakeErr, "certificate")
	})

	t.Run("a path does not change the transport", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials, wooltel.WithEndpointURL("https://"+addr+"/v1/traces")))
	})

	t.Run("the scheme is read case-insensitively", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials, wooltel.WithEndpointURL("HTTPS://"+addr)))
	})

	t.Run("WithInsecure does not downgrade an https URL", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials,
			wooltel.WithEndpointURL("https://"+addr), wooltel.WithInsecure()))
	})

	t.Run("the endpoint environment variable does not override the URL", func(t *testing.T) {
		envAddr, envDials := sniffingCollector(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", envAddr)
		addr, dials := sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials, wooltel.WithEndpointURL("https://"+addr)))
		select {
		case unexpected := <-envDials:
			require.Failf(t, "the environment endpoint was dialled", "%+v", unexpected)
		default:
		}
	})

	t.Run("the last endpoint option wins", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requirePlaintext(t, enableAndExport(t, dials,
			wooltel.WithEndpointURL("https://"+addr), wooltel.WithEndpoint(addr)))

		addr, dials = sniffingCollector(t)
		requireTLS(t, enableAndExport(t, dials,
			wooltel.WithEndpoint(addr), wooltel.WithEndpointURL("https://"+addr)))
	})
}

// TestEnable_PlaintextDefaultUnchanged pins the behaviour every caller that does
// not use WithEndpointURL already has: the endpoint is dialled in plaintext.
func TestEnable_PlaintextDefaultUnchanged(t *testing.T) {
	t.Run("WithEndpoint", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requirePlaintext(t, enableAndExport(t, dials, wooltel.WithEndpoint(addr)))
	})

	t.Run("WithEndpoint and WithInsecure", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		requirePlaintext(t, enableAndExport(t, dials, wooltel.WithEndpoint(addr), wooltel.WithInsecure()))
	})

	t.Run("the endpoint environment variable", func(t *testing.T) {
		addr, dials := sniffingCollector(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", addr)
		requirePlaintext(t, enableAndExport(t, dials))
	})
}

func TestWithEndpointURL_Refusals(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"empty", "", "empty"},
		{"whitespace", "   ", "URL"},
		{"no scheme, host and port", "localhost:4317", `scheme "localhost"`},
		{"no scheme, bare host", "collector.example.com", "no scheme"},
		{"no scheme, authority only", "//collector.example.com:4317", "no scheme"},
		{"grpc", "grpc://collector.example.com:4317", `scheme "grpc"`},
		{"grpcs", "grpcs://collector.example.com:4317", `scheme "grpcs"`},
		{"ftp", "ftp://collector.example.com", `scheme "ftp"`},
		{"unix", "unix:///var/run/collector.sock", `scheme "unix"`},
		{"http without a host", "http://", "no host"},
		{"https without a host", "https:///v1/traces", "no host"},
		{"https with only a port", "https://:4317", "no host"},
		{"not parseable", "http://exa mple.com:4317", "not a valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previousTracerProvider := otel.GetTracerProvider()
			previousTelemetry := wool.GetTelemetry()

			provider, err := wooltel.Enable(wooltel.WithEndpointURL(tc.url))
			require.Error(t, err)
			require.Nil(t, provider)
			require.ErrorContains(t, err, tc.want)

			// A refusal must leave both global registries exactly as they were.
			require.Same(t, previousTracerProvider, otel.GetTracerProvider())
			require.Equal(t, previousTelemetry, wool.GetTelemetry())
		})
	}

	t.Run("the error does not echo credentials in the URL", func(t *testing.T) {
		_, err := wooltel.Enable(wooltel.WithEndpointURL("ftp://user:hunter2@collector.example.com"))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "hunter2")
	})

	t.Run("an empty URL is refused even beside WithStdout", func(t *testing.T) {
		_, err := wooltel.Enable(wooltel.WithStdout(), wooltel.WithEndpointURL(""))
		require.Error(t, err)
	})

	t.Run("a later WithEndpoint supersedes a refused URL", func(t *testing.T) {
		// The URL option is not sticky: the last endpoint option decides, so a
		// caller that overrides a bad URL is not refused for it.
		previousTracerProvider := otel.GetTracerProvider()
		previousTelemetry := wool.GetTelemetry()
		backend, err := wooltel.Enable(wooltel.WithEndpointURL("ftp://x"), wooltel.WithEndpoint("127.0.0.1:1"))
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = backend.Shutdown(ctx)
			otel.SetTracerProvider(previousTracerProvider)
			wool.RegisterTelemetry(previousTelemetry)
		})
	})
}
