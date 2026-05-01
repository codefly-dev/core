package golang

import "encoding/json"

// unmarshalJSON is the indirection target for jsonUnmarshal. Kept in
// its own file so tests can override jsonUnmarshal without colliding
// with the production import of encoding/json elsewhere in this
// package.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
