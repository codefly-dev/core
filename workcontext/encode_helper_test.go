package workcontext_test

import "encoding/base64"

// encodePayload is the base64url the wire uses, for a test that assembles a
// token by hand.
func encodePayload(payload []byte) string { return base64.RawURLEncoding.EncodeToString(payload) }
