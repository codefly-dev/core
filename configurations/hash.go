package configurations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

func Hash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func HashString(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

type Hasher struct {
	hashes []string
}

func NewHasher() *Hasher {
	return &Hasher{}
}

func (h *Hasher) Add(s string) {
	h.hashes = append(h.hashes, HashString(s))
}

func (h *Hasher) Hash() string {
	var buf bytes.Buffer
	for _, hash := range h.hashes {
		buf.WriteString(hash)
	}
	return Hash(buf.Bytes())
}
