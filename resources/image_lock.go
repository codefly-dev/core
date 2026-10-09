package resources

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrImageUnpublished means an image lock names its image but records no
// digest: the image has not been published yet. A tree legitimately sits in
// that state between merging a service and publishing its first image, and the
// only safe reading is a refusal. A reference with no digest and no tag is the
// registry's :latest, which is whatever was pushed last. Callers add what to do
// about it by wrapping the error.
var ErrImageUnpublished = errors.New("image lock records no digest: the image has not been published")

// ImageLock is the recorded identity of a published image, the content of the
// <name>-image.json a service commits and embeds. It carries no tag: the tag is
// the service version, and a second copy of it is one more thing to disagree
// with the service's own manifest. Name is the full repository the image is
// published to, registry included.
type ImageLock struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// ParseImageLock returns the image a lock pins. name is the image's name inside
// ImageRegistry ("service-warehouse"); the lock must name exactly
// ImageRegistry/name. The registry comes from core, never from the lock: a lock
// naming another registry or another image is the lock being wrong, not an
// instruction to run an image from there.
//
// The result is addressed by digest alone (see DockerImage.FullName), so what
// runs, and what evidence describes, is the manifest that was checked and not
// whatever a tag points at by the time it is pulled. A lock with no digest is
// ErrImageUnpublished; one with a digest that is not a complete sha256 digest
// is a different error, so a caller can tell "not yet published" from "wrong".
func ParseImageLock(content []byte, name string) (*DockerImage, error) {
	var lock ImageLock
	if err := json.Unmarshal(content, &lock); err != nil {
		return nil, fmt.Errorf("parse image lock: %w", err)
	}
	image := PublishedImage(name, "")
	if want := image.Repository + "/" + image.Name; lock.Name != want {
		return nil, fmt.Errorf("image lock names %q, but the image is %q", lock.Name, want)
	}
	if lock.Digest == "" {
		return nil, ErrImageUnpublished
	}
	if !IsSHA256Digest(lock.Digest) {
		return nil, fmt.Errorf("image digest %q must be a sha256 digest", lock.Digest)
	}
	image.Digest = lock.Digest
	return &image, nil
}

// IsSHA256Digest reports whether digest is a complete sha256 image digest:
// "sha256:" followed by exactly 64 hexadecimal characters.
func IsSHA256Digest(digest string) bool {
	algorithm, encoded, found := strings.Cut(digest, ":")
	decoded, err := hex.DecodeString(encoded)
	return found && algorithm == "sha256" && err == nil && len(decoded) == 32
}
