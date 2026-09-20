package composition

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"time"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"google.golang.org/protobuf/encoding/protojson"
)

type ConsumerUsageStatement struct {
	Schema            string          `json:"schema"`
	Instance          string          `json:"instance"`
	CompositionDigest string          `json:"compositionDigest"`
	Pin               json.RawMessage `json:"pin"`
	Signer            string          `json:"signer"`
	ExpiresAt         time.Time       `json:"expiresAt"`
}

type SignedConsumerUsage struct {
	Statement []byte
	Signature []byte
}

type ConsumerUsageAuthority struct {
	Consumer string
	Signers  map[string]ed25519.PublicKey
}

func VerifyConsumerUsage(signed SignedConsumerUsage, authority ConsumerUsageAuthority, instance, compositionDigest string, now time.Time) (*updatev0.ConsumerPin, error) {
	var statement ConsumerUsageStatement
	if err := decodeStrictJSON(signed.Statement, &statement); err != nil {
		return nil, err
	}
	key := authority.Signers[statement.Signer]
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, signed.Statement, signed.Signature) {
		return nil, ErrSignature
	}
	if statement.Schema != "codefly/consumer-usage/v1" || statement.Instance != instance ||
		!digestPattern.MatchString(compositionDigest) || statement.CompositionDigest != compositionDigest ||
		now.IsZero() || !statement.ExpiresAt.After(now) {
		return nil, errors.New("consumer usage is expired or belongs to different composition inputs or instance")
	}
	pin := new(updatev0.ConsumerPin)
	if err := protojson.Unmarshal(statement.Pin, pin); err != nil {
		return nil, err
	}
	if authority.Consumer == "" || pin.Consumer != authority.Consumer || pin.SchemaVersion != 1 || !pin.UsageComplete {
		return nil, errors.New("consumer usage does not identify the authorized consumer with complete coverage")
	}
	return pin, nil
}
