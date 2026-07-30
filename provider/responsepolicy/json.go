package responsepolicy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Limits bounds every dimension an untrusted vendor response can grow along.
// Zero fields are rejected by Validate so a caller cannot accidentally admit an
// unbounded response.
type Limits struct {
	MaxCompressedBytes   int64
	MaxDecompressedBytes int64
	MaxDepth             int
	MaxKeys              int
	MaxArrayLen          int
	MaxStringBytes       int
}

// DefaultLimits are conservative bounds for JSON management APIs.
func DefaultLimits() Limits {
	return Limits{
		MaxCompressedBytes:   1 << 20,
		MaxDecompressedBytes: 8 << 20,
		MaxDepth:             64,
		MaxKeys:              10000,
		MaxArrayLen:          10000,
		MaxStringBytes:       1 << 20,
	}
}

func (l Limits) validate() error {
	if l.MaxCompressedBytes <= 0 || l.MaxDecompressedBytes <= 0 || l.MaxDepth <= 0 ||
		l.MaxKeys <= 0 || l.MaxArrayLen <= 0 || l.MaxStringBytes <= 0 {
		return fmt.Errorf("response limits must all be positive")
	}
	return nil
}

// decodeBody bounds and, if needed, decompresses a response body, then parses
// it into a normalized value tree. It owns Accept-Encoding decoding entirely:
// only identity and gzip are accepted, both bounded against decompression
// bombs.
func decodeBody(raw []byte, contentEncoding string, limits Limits) (any, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if int64(len(raw)) > limits.MaxCompressedBytes {
		return nil, fmt.Errorf("response exceeds compressed byte budget")
	}
	var reader io.Reader = bytes.NewReader(raw)
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("response gzip is invalid: %w", err)
		}
		reader = zr
	default:
		return nil, fmt.Errorf("response content encoding %q is not admitted", contentEncoding)
	}
	// Read one byte past the budget so an exact-size bomb is still detected.
	bounded := io.LimitReader(reader, limits.MaxDecompressedBytes+1)
	decoded, err := io.ReadAll(bounded)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(decoded)) > limits.MaxDecompressedBytes {
		return nil, fmt.Errorf("response exceeds decompressed byte budget")
	}
	return parseJSON(decoded, limits)
}

// parseJSON decodes JSON while rejecting duplicate object keys and enforcing
// structural limits. It uses the streaming token API so a duplicate key is
// caught before any value is materialized — encoding/json's default decoder
// silently keeps the last of duplicate keys, which is an ambiguity an attacker
// can exploit.
func parseJSON(data []byte, limits Limits) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	counter := &keyCounter{max: limits.MaxKeys}
	value, err := parseValue(decoder, limits, 1, counter)
	if err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("response contains trailing data")
	}
	return value, nil
}

type keyCounter struct {
	seen int
	max  int
}

func (c *keyCounter) add() error {
	c.seen++
	if c.seen > c.max {
		return fmt.Errorf("response exceeds object key budget")
	}
	return nil
}

func parseValue(decoder *json.Decoder, limits Limits, depth int, counter *keyCounter) (any, error) {
	if depth > limits.MaxDepth {
		return nil, fmt.Errorf("response exceeds nesting budget")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parseFromToken(token, decoder, limits, depth, counter)
}

func parseFromToken(token json.Token, decoder *json.Decoder, limits Limits, depth int, counter *keyCounter) (any, error) {
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseObject(decoder, limits, depth, counter)
		case '[':
			return parseArray(decoder, limits, depth, counter)
		default:
			return nil, fmt.Errorf("response contains unbalanced delimiter")
		}
	case string:
		if len(t) > limits.MaxStringBytes {
			return nil, fmt.Errorf("response exceeds string byte budget")
		}
		return t, nil
	case json.Number, bool, nil:
		return t, nil
	default:
		return nil, fmt.Errorf("response contains an unsupported token")
	}
}

func parseObject(decoder *json.Decoder, limits Limits, depth int, counter *keyCounter) (any, error) {
	object := make(map[string]any)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode response object key: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("response object key is not a string")
		}
		if err := counter.add(); err != nil {
			return nil, err
		}
		if _, duplicate := object[key]; duplicate {
			return nil, fmt.Errorf("response contains duplicate object key %q", key)
		}
		value, err := parseValue(decoder, limits, depth+1, counter)
		if err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := decoder.Token(); err != nil { // consume '}'
		return nil, fmt.Errorf("decode response object end: %w", err)
	}
	return object, nil
}

func parseArray(decoder *json.Decoder, limits Limits, depth int, counter *keyCounter) (any, error) {
	array := make([]any, 0)
	for decoder.More() {
		if len(array) >= limits.MaxArrayLen {
			return nil, fmt.Errorf("response exceeds array length budget")
		}
		value, err := parseValue(decoder, limits, depth+1, counter)
		if err != nil {
			return nil, err
		}
		array = append(array, value)
	}
	if _, err := decoder.Token(); err != nil { // consume ']'
		return nil, fmt.Errorf("decode response array end: %w", err)
	}
	return array, nil
}

// canonicalJSON reserializes a value tree with sorted object keys so equal
// safe results serialize identically for cassettes and digests.
func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := writeCanonical(&buffer, value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeCanonical(buffer *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buffer.WriteByte(',')
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buffer.Write(encoded)
			buffer.WriteByte(':')
			if err := writeCanonical(buffer, v[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
		return nil
	case []any:
		buffer.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonical(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
		return nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
		return nil
	}
}
