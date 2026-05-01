// Package javascript runs and parses test output for JS/TS test
// runners — vitest, jest, playwright. Produces the SOTA structured
// runtimev0.TestResponse the codefly platform expects (see
// proto/codefly/services/runtime/v0/runtime.proto).
//
// Three parsers, two schemas:
//
//   - ParseJestVitestJSON  — vitest and jest emit nearly-identical
//                            JSON (vitest copied jest's reporter
//                            shape). One parser handles both.
//                            `--reporter=json` (vitest) or `--json`
//                            (jest); both produce the same envelope.
//
//   - ParsePlaywrightJSON  — playwright emits its own shape with
//                            nested suites + per-test retry results.
//                            `--reporter=json`.
//
// Output retention rule (load-bearing): same as the python+go
// runners. PASSED + SKIPPED cases get empty captured_output;
// FAILED + ERRORED cases carry failure detail up to the per-case
// cap. JS test runners' JSON envelopes already follow this pattern
// — failureMessages array is empty for passed tests, populated for
// failed.
//
// All parsers feed StructuredTestRun, the same convertible-to-proto
// type the python and go parsers use. ToProtoResponse(runner,
// suiteName, duration) returns runtimev0.TestResponse with both the
// structured tree and the legacy flat fields populated.
package javascript
