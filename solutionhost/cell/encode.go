package cell

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Encode is the ONE way to write a cell: validate, marshal, and read the bytes
// back through this package's own reader, returning them only when what comes
// back is what went in.
//
// A publisher that marshals a File directly can write a cell this reader
// refuses — an image digest of the wrong length marshals happily and fails on
// the next parse — and the delivery repository then holds a document the
// platform's loader rejects, discovered at admission rather than at publish.
// It can also write one that parses to something ELSE, which no reader
// refuses. So the guard runs both ways, and a writer gets bytes or an error.
//
// Nothing else in this package writes a cell, and nothing outside it should
// marshal the model by hand.
func (file *File) Encode() ([]byte, error) {
	if err := file.Validate(); err != nil {
		return nil, fmt.Errorf("a cell is validated before it is written: %w", err)
	}
	document, err := yaml.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("%w: the cell cannot be encoded: %v", ErrInvalid, err)
	}
	again, err := Parse(document)
	if err != nil {
		return nil, fmt.Errorf("%w: the encoded cell is refused by its own reader, so it is not written: %v", ErrInvalid, err)
	}
	if !reflect.DeepEqual(file, again) {
		return nil, fmt.Errorf("%w: the encoded cell reads back as a different cell, so it is not written", ErrInvalid)
	}
	return document, nil
}
