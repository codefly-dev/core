package modulecontract

import (
	"bytes"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Encode is the ONE way to write a module contract: validate, marshal, and
// read the bytes back through this package's own reader, returning them only
// when what comes back is what went in.
//
// A publisher that marshals a Contract directly can emit a document this
// reader refuses — a principal of "INVALID PRINCIPAL" marshals happily and
// fails on the next parse — and the failure then surfaces at whoever reads the
// file rather than at whoever wrote it. It can also emit a document that parses
// to something ELSE, which is worse: no reader refuses it and no one notices.
// So the guard is both directions, and a writer gets bytes or an error.
//
// Nothing else in this package writes a contract, and nothing outside it
// should marshal the model by hand.
func (contract *Contract) Encode() ([]byte, error) {
	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("a module contract is validated before it is written: %w", err)
	}
	document, err := yaml.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("%w: the contract cannot be encoded: %v", ErrInvalid, err)
	}
	again, err := Parse(document)
	if err != nil {
		return nil, fmt.Errorf("%w: the encoded contract is refused by its own reader, so it is not written: %v", ErrInvalid, err)
	}
	if !reflect.DeepEqual(contract, again) {
		return nil, fmt.Errorf("%w: the encoded contract reads back as a different contract, so it is not written", ErrInvalid)
	}
	return document, nil
}

// EncodeTo writes the contract into w through Encode, so a caller cannot
// stream an unvalidated model.
func (contract *Contract) EncodeTo(w *bytes.Buffer) error {
	document, err := contract.Encode()
	if err != nil {
		return err
	}
	_, err = w.Write(document)
	return err
}
