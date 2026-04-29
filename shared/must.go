package shared

// Must collapses an `(T, error)` return into `T`, panicking on error.
//
// USE SPARINGLY. Appropriate at:
//   - init() / package-level globals where failure means the program
//     can't start (e.g. embed.FS parsing, regexp.MustCompile-style).
//   - test code, where panic == test failure.
//
// DO NOT use in request-path or runtime code — wrap with wool.Wrapf
// and propagate the error instead. A panic in an agent's gRPC handler
// is harder to debug than a clean error return, and `defer Wool.Catch()`
// only converts panics to opaque internal-server-errors.
func Must[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}
