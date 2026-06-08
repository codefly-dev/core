package rust

import (
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// sampleOutput is representative `cargo test` text: one unit-test binary with a
// pass, a fail (with a panic detail block) and an ignored test, plus an
// integration binary with a single pass.
const sampleOutput = `   Compiling demo v0.1.0 (/work/demo)
    Finished ` + "`test`" + ` profile [unoptimized + debuginfo] target(s) in 0.42s
     Running unittests src/lib.rs (target/debug/deps/demo-1a2b3c4d)

running 3 tests
test tests::it_adds ... ok
test tests::it_fails ... FAILED
test tests::it_is_skipped ... ignored

failures:

---- tests::it_fails stdout ----
thread 'tests::it_fails' panicked at src/lib.rs:12:9:
assertion ` + "`left == right`" + ` failed
  left: 2
  right: 3

failures:
    tests::it_fails

test result: FAILED. 1 passed; 1 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s

     Running tests/integration.rs (target/debug/deps/integration-9f8e7d6c)

running 1 test
test integration_smoke ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
`

func TestParseCargoTest(t *testing.T) {
	s := ParseCargoTest(sampleOutput)
	if s.Run != 4 {
		t.Errorf("Run = %d, want 4", s.Run)
	}
	if s.Passed != 2 {
		t.Errorf("Passed = %d, want 2", s.Passed)
	}
	if s.Failed != 1 {
		t.Errorf("Failed = %d, want 1", s.Failed)
	}
	if s.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", s.Skipped)
	}
	if len(s.Failures) != 1 {
		t.Fatalf("Failures = %d, want 1", len(s.Failures))
	}
	if want := "left: 2"; !contains(s.Failures[0], want) {
		t.Errorf("failure detail missing %q:\n%s", want, s.Failures[0])
	}
}

func TestParseCargoTestStructured(t *testing.T) {
	r := ParseCargoTestStructured(sampleOutput)
	if len(r.Suites) != 2 {
		t.Fatalf("Suites = %d, want 2", len(r.Suites))
	}
	resp := r.ToProtoResponse("cargo-test", "demo", 0)

	if resp.GetCounts().GetTotal() != 4 {
		t.Errorf("Total = %d, want 4", resp.GetCounts().GetTotal())
	}
	if resp.GetCounts().GetPassed() != 2 {
		t.Errorf("Passed = %d, want 2", resp.GetCounts().GetPassed())
	}
	if resp.GetCounts().GetFailed() != 1 {
		t.Errorf("Failed = %d, want 1", resp.GetCounts().GetFailed())
	}
	if resp.GetCounts().GetSkipped() != 1 {
		t.Errorf("Skipped = %d, want 1", resp.GetCounts().GetSkipped())
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_FAILED {
		t.Errorf("run state = %v, want FAILED", resp.GetResult().GetState())
	}

	// The failing case must carry a panic-kind failure and detail.
	var found *runtimev0.TestCase
	for _, suite := range resp.GetSuites() {
		for _, c := range suite.GetCases() {
			if c.GetName() == "tests::it_fails" {
				found = c
			}
		}
	}
	if found == nil {
		t.Fatal("did not find failing case tests::it_fails")
	}
	if found.GetState() != runtimev0.TestCaseState_TEST_CASE_STATE_FAILED {
		t.Errorf("case state = %v, want FAILED", found.GetState())
	}
	if found.GetFailure().GetKind() != runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC {
		t.Errorf("failure kind = %v, want PANIC", found.GetFailure().GetKind())
	}
	if !contains(found.GetCapturedOutput(), "assertion") {
		t.Errorf("captured output missing assertion detail: %q", found.GetCapturedOutput())
	}
}

func TestParseCargoBinaryName(t *testing.T) {
	cases := map[string]string{
		`[package]
name = "my-service"
version = "0.1.0"`: "my-service",
		// [[bin]] name takes precedence over [package] name.
		`[package]
name = "lib-name"

[[bin]]
name = "server-bin"
path = "src/main.rs"`: "server-bin",
	}
	for toml, want := range cases {
		if got := parseCargoBinaryName(toml); got != want {
			t.Errorf("parseCargoBinaryName() = %q, want %q", got, want)
		}
	}
}

func TestStreamingTestWriter(t *testing.T) {
	var events []TestEvent
	w := &StreamingTestWriter{OnEvent: func(e TestEvent) { events = append(events, e) }}
	for _, line := range splitLines(sampleOutput) {
		_, _ = w.Write([]byte(line))
	}
	// 4 test lines → 4 events.
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	// First event is a pass in the unittests suite.
	if events[0].Action != "pass" || events[0].Test != "tests::it_adds" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Action != "fail" {
		t.Errorf("event[1].Action = %q, want fail", events[1].Action)
	}
	if events[3].Suite != "tests/integration.rs" {
		t.Errorf("event[3].Suite = %q, want tests/integration.rs", events[3].Suite)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
