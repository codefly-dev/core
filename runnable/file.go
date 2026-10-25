package runnable

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// A FILE field carries a document across the call boundary without a path or
// a URL ever crossing it. The caller — the side holding the person's
// authority — resolves the document reference before the call and places the
// bytes in the payload; the implementation reads only what it was handed. What
// an implementation returns is bytes again, which the caller publishes as a
// new document or as a new version of an input document: a runnable never
// writes a document in place, and never names where one lives.
//
// The wire value is one closed JSON object. FileValue is its one Go spelling;
// the Python harness holds the same members (runnable-python's schema.py).

// The members of a FILE value, as they are spelled on the wire.
const (
	FileReferenceMember = "reference"
	FileMediaTypeMember = "media_type"
	FileNameMember      = "name"
	FileContentMember   = "content"
)

// Bounds on the metadata of a FILE value. The content is bounded by the
// operation's payload bound, which is measured on the whole document.
const (
	// MaxFileReferenceBytes bounds an opaque document reference.
	MaxFileReferenceBytes = 1024
	// MaxFileNameBytes bounds a display name.
	MaxFileNameBytes = 255
)

// FileValue is one document as a call carries it. Reference is the caller's
// opaque identity of a stored document version: a runnable may echo it and
// never dereferences it, since it has neither the network nor the authority
// to. Content is the document's bytes.
type FileValue struct {
	Reference string `json:"reference,omitempty"`
	MediaType string `json:"media_type"`
	Name      string `json:"name,omitempty"`
	Content   []byte `json:"content"`
}

// FileSide says which payload a FILE value travels in, because the two differ
// in one rule: an input names the document it was read from, an output names
// at most the input it is a new version of.
type FileSide int

const (
	// FileInput is a value the caller staged for the call.
	FileInput FileSide = iota
	// FileOutput is a value the implementation returned.
	FileOutput
)

// DecodeFileValue reads one FILE value strictly — an undeclared member, a
// repeated member, content that is not standard padded base64 and a value
// that is not an object are each refused — and checks it against the field
// that declares it. inputs are the references of the call's input FILE
// values; an output's reference must be one of them. For an input, pass nil.
func DecodeFileValue(field *basev0.RunnableField, side FileSide, raw json.RawMessage, inputs []string) (*FileValue, error) {
	if field.GetType() != basev0.RunnableField_FILE {
		return nil, fmt.Errorf("%w: field %q is %s, not FILE", ErrInvalid, field.GetName(), field.GetType())
	}
	members, err := closedObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: FILE value: %v", ErrInvalid, err)
	}
	value := &FileValue{}
	for key, member := range members {
		var text string
		if err := json.Unmarshal(member, &text); err != nil {
			return nil, fmt.Errorf("%w: FILE value member %q must be a string", ErrInvalid, key)
		}
		switch key {
		case FileReferenceMember:
			value.Reference = text
		case FileMediaTypeMember:
			value.MediaType = text
		case FileNameMember:
			value.Name = text
		case FileContentMember:
			// StdEncoding is padded and refuses characters outside the
			// alphabet, so one document has one spelling of its bytes.
			decoded, err := base64.StdEncoding.Strict().DecodeString(text)
			if err != nil {
				return nil, fmt.Errorf("%w: FILE value content is not standard padded base64: %v", ErrInvalid, err)
			}
			value.Content = decoded
		default:
			return nil, fmt.Errorf("%w: FILE value member %q is not one of %s, %s, %s, %s", ErrInvalid, key,
				FileReferenceMember, FileMediaTypeMember, FileNameMember, FileContentMember)
		}
	}
	if _, present := members[FileContentMember]; !present {
		return nil, fmt.Errorf("%w: FILE value carries no %s", ErrInvalid, FileContentMember)
	}
	if err := CheckFileValue(field, side, value, inputs); err != nil {
		return nil, err
	}
	return value, nil
}

// CheckFileValue holds a decoded value to its field: an admitted media type, a
// bounded display name that is not a path, and the reference rule of its side.
func CheckFileValue(field *basev0.RunnableField, side FileSide, value *FileValue, inputs []string) error {
	if value == nil {
		return fmt.Errorf("%w: FILE value is required", ErrInvalid)
	}
	if !slices.Contains(field.GetMediaTypes(), value.MediaType) {
		return fmt.Errorf("%w: FILE %q media type %q is not one of %v", ErrInvalid, field.GetName(), value.MediaType, field.GetMediaTypes())
	}
	if value.Name != "" && !fileNameAdmitted(value.Name) {
		return fmt.Errorf("%w: FILE %q name must be at most %d bytes of printable text without a path separator", ErrInvalid, field.GetName(), MaxFileNameBytes)
	}
	switch side {
	case FileInput:
		if !fileReferenceAdmitted(value.Reference) {
			return fmt.Errorf("%w: input FILE %q must carry a reference of 1..%d bytes without whitespace or control characters", ErrInvalid, field.GetName(), MaxFileReferenceBytes)
		}
	case FileOutput:
		if value.Reference != "" && !slices.Contains(inputs, value.Reference) {
			return fmt.Errorf("%w: output FILE %q names reference %q, which is no input document of this call; an output is a new document or a new version of one it was handed", ErrInvalid, field.GetName(), value.Reference)
		}
	default:
		return fmt.Errorf("%w: unknown FILE side %d", ErrInvalid, side)
	}
	return nil
}

func fileReferenceAdmitted(reference string) bool {
	if reference == "" || len(reference) > MaxFileReferenceBytes || !utf8.ValidString(reference) {
		return false
	}
	return !strings.ContainsFunc(reference, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func fileNameAdmitted(name string) bool {
	if len(name) > MaxFileNameBytes || !utf8.ValidString(name) || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r == '/' || r == '\\' || unicode.IsControl(r) })
}

// closedObject decodes one JSON object into its members, refusing a repeated
// key rather than keeping the last one: two spellings of one member are two
// documents, and the reader that picked one would disagree with a reader that
// picked the other.
func closedObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, fmt.Errorf("must be an object")
	}
	members := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		if _, repeated := members[key]; repeated {
			return nil, fmt.Errorf("member %q is repeated", key)
		}
		var member json.RawMessage
		if err := decoder.Decode(&member); err != nil {
			return nil, err
		}
		members[key] = member
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("trailing data after the object")
	}
	return members, nil
}
