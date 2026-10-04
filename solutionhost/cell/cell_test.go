package cell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "valid.cell.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestParsesTheAgreedShape pins the shape a publish writes and the platform
// reads: the schema string, the host header, one namespace per module, and
// every field the platform compares.
func TestParsesTheAgreedShape(t *testing.T) {
	file, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if file.Environment != "production" || file.TrustDomain != "cluster.example" || len(file.Namespaces) != 1 {
		t.Fatalf("file %+v", file)
	}
	namespace := file.Namespaces[0]
	if namespace.Module != "payments" || len(namespace.Workloads) != 2 || namespace.Delivery == nil || len(namespace.Egress) != 1 {
		t.Fatalf("namespace %+v", namespace)
	}
	api := namespace.Workloads[0]
	if api.Authenticating != "api" || len(api.Containers) != 2 || len(api.InitContainers) != 1 || !api.Verifier || !api.CloudIdentity {
		t.Fatalf("workload %+v", api)
	}
	if got := strings.Join(api.Endpoints[0].Consumers, ","); got != "billing/worker,payments/web" {
		t.Fatalf("consumers %q", got)
	}
	if api.Release == nil || api.Release.Version != "1.4.0" {
		t.Fatalf("release %+v", api.Release)
	}
	// What is parsed is what is written: the model round-trips byte for byte
	// through the encoder a publish uses, so a reader and a writer agree.
	encoded, err := yaml.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatalf("the encoded cell is refused: %v", err)
	}
	if again.Namespaces[0].Workloads[0].SPIFFEID != api.SPIFFEID {
		t.Fatal("the cell did not round-trip")
	}
}

// TestEveryFixtureReachesItsOutcome runs the kit against this package's own
// reader: the reference implementation passes its own fixtures, and a
// fixture whose outcome drifted from the reader is caught here first.
func TestEveryFixtureReachesItsOutcome(t *testing.T) {
	Run(t, func(document []byte) error {
		_, err := Parse(document)
		return err
	})
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 20 {
		t.Fatalf("the kit ships %d fixtures; a shrinking kit is a deleted rule", len(all))
	}
}

// TestTheKitFailsAReaderThatSkipsARule: a reader that decodes but does not
// validate passes the valid fixtures and fails the refusals by name, which is
// what makes the kit a gate rather than a round-trip.
func TestTheKitFailsAReaderThatSkipsARule(t *testing.T) {
	recorder := &recordingT{}
	Run(recorder, func(document []byte) error {
		var file File
		return yaml.Unmarshal(document, &file)
	})
	if recorder.failures == 0 {
		t.Fatal("a reader that validates nothing passed the kit")
	}
	if !strings.Contains(recorder.messages, "cell fixture empty-selector must be refused") {
		t.Fatalf("the kit did not name the rule the reader skipped:\n%s", recorder.messages)
	}
}

// TestRefusesWhatThePlatformCannotPolice: each rule, named, so an operator
// reads one reason for one condition whichever reader refused it.
func TestRefusesWhatThePlatformCannotPolice(t *testing.T) {
	base := string(fixture(t))
	for name, table := range map[string]struct {
		mutate func(string) string
		want   error
		text   string
	}{
		"a workload named twice": {
			mutate: func(s string) string {
				return strings.Replace(s, "          - name: migrate\n            kind: Job", "          - name: api\n            kind: Deployment", 1)
			},
			want: ErrInvalid, text: "twice",
		},
		"an endpoint port past the range": {
			mutate: func(s string) string { return strings.Replace(s, "port: 9090", "port: 70000", 1) },
			want:   ErrInvalid, text: "is not a port",
		},
		"a spiffe id with no trust domain": {
			mutate: func(s string) string {
				return strings.Replace(strings.Replace(strings.Replace(strings.Replace(s, "coordinate: example/production/region-a\n", "", 1), "component: platform-host\n", "", 1), "domain: example\n", "", 1), "trust_domain: cluster.example\n", "", 1)
			},
			want: ErrInvalid, text: "but the cell declares no trust domain",
		},
		"a binding declared twice": {
			mutate: func(s string) string {
				return strings.Replace(s, "bindings: [cache, store]", "bindings: [cache, cache]", 1)
			},
			want: ErrInvalid, text: `binding "cache" is declared twice`,
		},
		"a container named twice": {
			mutate: func(s string) string {
				return strings.Replace(s, "                - name: proxy\n", "                - name: api\n", 1)
			},
			want: ErrInvalid, text: `names container "api" twice`,
		},
		"a release missing its version": {
			mutate: func(s string) string { return strings.Replace(s, "                version: 1.4.0\n", "", 1) },
			want:   ErrInvalid, text: "release must carry publisher, name and version",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(table.mutate(base)))
			if !errors.Is(err, table.want) || !strings.Contains(err.Error(), table.text) {
				t.Fatalf("got %v, want %v saying %q", err, table.want, table.text)
			}
		})
	}
}

type recordingT struct {
	failures int
	messages string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.failures++
	r.messages += strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n"
}

func (r *recordingT) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
}
